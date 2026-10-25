package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pgxtx"
	"github.com/azverinus/flagnetic/internal/pkg/safego"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ChangesChannel is the notification channel audit entries are announced
// on, each as {project}:{entry}.
const ChangesChannel = "flagnetic_management_change"

// Announce announces e on ChangesChannel in the unit of work ctx carries.
// The announcement names the entry rather than carrying its notice: a
// notification's payload is bounded, and every replica reads the entries
// announced together in one query anyway.
func (l *AuditLog) Announce(ctx context.Context, e governance.Entry) error {
	_, err := pgxtx.Querier(ctx, l.pool).Exec(ctx, `SELECT pg_notify($1, $2)`,
		ChangesChannel, e.Project.String()+":"+e.ID.String())
	if err != nil {
		return fmt.Errorf("announcing audit entry %s: %w", e.Action, err)
	}
	return nil
}

// noticeColumns read an entry's notice. The environment is named by its
// key, as clients address it; an environment's own entries name it by the
// entry's key, which outlives its deletion.
const noticeColumns = `a.id, a.entity_kind, a.entity_id, a.entity_key,
	coalesce(e.key, CASE WHEN a.entity_kind = 'environment' THEN a.entity_key END, ''),
	a.actor_kind, a.actor_id, a.actor_name, coalesce(a.actor_member_id::text, ''),
	coalesce(a.after -> 'version', a.before -> 'version')
	FROM audit_entries a LEFT JOIN environments e ON e.id = a.environment_id`

func scanNotice(row pgx.CollectableRow) (governance.ChangeNotice, error) {
	var (
		n       governance.ChangeNotice
		entryID [16]byte
		kind    string
		version []byte
	)
	if err := row.Scan(&entryID, &n.Entity.Kind, &n.Entity.ID, &n.Entity.Key, &n.Environment,
		&kind, &n.Actor.ID, &n.Actor.Name, &n.Actor.MemberID, &version); err != nil {
		return governance.ChangeNotice{}, err
	}
	n.Entry, n.Actor.Kind = id.FromBytes[governance.Entry](entryID), governance.ActorKind(kind)
	// A version is a number wherever an entity carries one; anything else
	// is no version.
	var v int64
	if version != nil && json.Unmarshal(version, &v) == nil {
		n.Version = &v
	}
	return n, nil
}

// Notices returns the notices of project's entries with entryIDs, oldest
// first; an identifier the project holds no entry with is left out.
func (l *AuditLog) Notices(ctx context.Context, project workspace.ProjectID, entryIDs []governance.EntryID) ([]governance.ChangeNotice, error) {
	ids := make([][16]byte, len(entryIDs))
	for i, e := range entryIDs {
		ids[i] = e.Bytes()
	}
	rows, err := pgxtx.Querier(ctx, l.pool).Query(ctx, `SELECT `+noticeColumns+`
		WHERE a.project_id = $1 AND a.id = ANY($2) ORDER BY a.id`, project.Bytes(), ids)
	if err != nil {
		return nil, fmt.Errorf("reading change notices: %w", err)
	}
	notices, err := pgx.CollectRows(rows, scanNotice)
	if err != nil {
		return nil, fmt.Errorf("reading change notices: %w", err)
	}
	return notices, nil
}

// NoticesAfter returns the notices of project's entries after the one with
// after, oldest first, at most limit; ok is false when project holds no
// entry with after or more than limit follow it.
//
// Identifiers order entries by when they were recorded, not committed, so
// an entry committed after after's with an earlier identifier is not
// replayed: it reached the stream the client held, unless that one had
// dropped already, and a client reconnecting refetches what it shows then.
func (l *AuditLog) NoticesAfter(ctx context.Context, project workspace.ProjectID, after governance.EntryID, limit int) ([]governance.ChangeNotice, bool, error) {
	q := pgxtx.Querier(ctx, l.pool)
	var known bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT FROM audit_entries WHERE project_id = $1 AND id = $2)`,
		project.Bytes(), after.Bytes()).Scan(&known); err != nil {
		return nil, false, fmt.Errorf("finding the entry to replay after: %w", err)
	}
	if !known {
		return nil, false, nil
	}
	rows, err := q.Query(ctx, `SELECT `+noticeColumns+`
		WHERE a.project_id = $1 AND a.id > $2 ORDER BY a.id LIMIT $3`, project.Bytes(), after.Bytes(), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("replaying change notices: %w", err)
	}
	notices, err := pgx.CollectRows(rows, scanNotice)
	if err != nil {
		return nil, false, fmt.Errorf("replaying change notices: %w", err)
	}
	if len(notices) > limit {
		return nil, false, nil
	}
	return notices, true, nil
}

// fanOutTimeout bounds one read of a fan-out cycle; a cycle that cannot
// read tells its streams to refetch.
const fanOutTimeout = 30 * time.Second

// ChangeHub is one replica's fan-out of announced audit entries to its
// management change streams, standing on ChangesChannel. Per project with
// streams, one fan-out reads the entries announced since its last cycle in
// one query and offers their notices to every stream of the project;
// announcements arriving during a read make one more cycle after it.
//
// While the replica does not listen an announcement may be lost, so every
// stream open then, or opened then, is told to refetch once it listens
// again.
type ChangeHub struct {
	log    *AuditLog
	logger *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	runs   sync.WaitGroup

	mu        sync.Mutex
	closed    bool
	listening bool
	projects  map[workspace.ProjectID]*followed
}

// followed is the streams of one project and its fan-out.
type followed struct {
	followers map[*follower]struct{}
	// announced are the entries announced since the last cycle began.
	announced []governance.EntryID
	// running is set while a fan-out runs.
	running bool
}

// NewChangeHub returns the hub reading notices from log.
func NewChangeHub(log *AuditLog, logger *slog.Logger) *ChangeHub {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &ChangeHub{log: log, logger: logger, projects: map[workspace.ProjectID]*followed{}}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	return h
}

// Close ends the fan-outs and waits for them; nothing is offered after it.
func (h *ChangeHub) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.cancel()
	h.runs.Wait()
}

// Follow starts handing a stream the notices of project. A stream that
// starts following while the replica does not listen is told to refetch
// once it does.
func (h *ChangeHub) Follow(project workspace.ProjectID) appgovernance.ChangeFollower {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.projects[project]
	if !ok {
		t = &followed{followers: map[*follower]struct{}{}}
		h.projects[project] = t
	}
	f := &follower{hub: h, project: project, topic: t, changed: make(chan struct{}, 1), missed: !h.listening}
	t.followers[f] = struct{}{}
	return f
}

// Listening records whether announcements arrive. Once they do again,
// every stream that may have missed one is told to refetch.
func (h *ChangeHub) Listening(up bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listening = up
	for _, t := range h.projects {
		for f := range t.followers {
			f.listening(up)
		}
	}
}

// Received records an announced entry for its project's next fan-out. A
// project no stream of this replica follows is ignored.
func (h *ChangeHub) Received(payload string) {
	project, entry, err := parseAnnouncement(payload)
	if err != nil {
		h.logger.Warn("ignoring a malformed change announcement", "payload", payload, "error", err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.projects[project]
	if !ok {
		return
	}
	t.announced = append(t.announced, entry)
	if t.running || h.closed {
		return
	}
	t.running = true
	h.runs.Add(1)
	safego.Go(h.ctx, "change fan-out", h.logger, func(ctx context.Context) {
		defer h.runs.Done()
		ended := false
		defer func() {
			if !ended {
				// A fan-out that panicked must not leave its project
				// waiting for it forever: its streams refetch, and the
				// next announcement starts another.
				h.mu.Lock()
				t.running, t.announced = false, nil
				for f := range t.followers {
					f.offer(nil, true)
				}
				h.drop(project, t)
				h.mu.Unlock()
			}
		}()
		h.fanOut(ctx, project, t)
		ended = true
	})
}

// fanOut runs cycles until no announcement arrived during the last.
func (h *ChangeHub) fanOut(ctx context.Context, project workspace.ProjectID, t *followed) {
	for {
		h.mu.Lock()
		entries := t.announced
		t.announced = nil
		if len(entries) == 0 || ctx.Err() != nil {
			t.running = false
			h.drop(project, t)
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()

		readCtx, cancel := context.WithTimeout(ctx, fanOutTimeout)
		notices, err := h.log.Notices(readCtx, project, entries)
		cancel()
		// A read cut short by Close is no failure worth telling.
		if err != nil && ctx.Err() == nil {
			h.logger.Warn("change notices could not be read; their streams refetch",
				"project", project.String(), "error", err.Error())
		}

		h.mu.Lock()
		for f := range t.followers {
			f.offer(notices, err != nil)
		}
		h.mu.Unlock()
	}
}

// drop removes t once no stream follows it and no fan-out runs for it. h.mu
// is held.
func (h *ChangeHub) drop(project workspace.ProjectID, t *followed) {
	if len(t.followers) == 0 && !t.running && h.projects[project] == t {
		delete(h.projects, project)
	}
}

func parseAnnouncement(payload string) (workspace.ProjectID, governance.EntryID, error) {
	p, e, ok := strings.Cut(payload, ":")
	if !ok {
		return workspace.ProjectID{}, governance.EntryID{}, fmt.Errorf("want {project}:{entry}")
	}
	project, err := id.Parse[workspace.Project](p)
	if err != nil {
		return workspace.ProjectID{}, governance.EntryID{}, err
	}
	entry, err := id.Parse[governance.Entry](e)
	if err != nil {
		return workspace.ProjectID{}, governance.EntryID{}, err
	}
	return project, entry, nil
}

// follower is one stream's following of its project.
type follower struct {
	hub     *ChangeHub
	project workspace.ProjectID
	topic   *followed
	changed chan struct{}

	mu      sync.Mutex
	pending governance.PendingNotices
	// missed is set while the replica does not listen: an announcement
	// made meanwhile is lost, so the stream refetches once it listens
	// again.
	missed bool
}

func (f *follower) Changed() <-chan struct{} { return f.changed }

func (f *follower) Take() ([]governance.ChangeNotice, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending.Take()
}

// Close stops following, and removes the project with its last stream
// unless a fan-out still runs for it.
func (f *follower) Close() {
	h := f.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(f.topic.followers, f)
	h.drop(f.project, f.topic)
}

// offer adds notices to those waiting, after a refetch when refetch is set,
// and signals the stream.
func (f *follower) offer(notices []governance.ChangeNotice, refetch bool) {
	f.mu.Lock()
	if refetch {
		f.pending.Refetch()
	}
	f.pending.Offer(notices...)
	f.mu.Unlock()
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

// listening records whether the replica listens.
func (f *follower) listening(up bool) {
	f.mu.Lock()
	missed := f.missed
	f.missed = !up
	f.mu.Unlock()
	if up && missed {
		f.offer(nil, true)
	}
}
