package governance

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ChangeAnnouncer announces audit entries to the management change stream
// of every replica.
type ChangeAnnouncer interface {
	// Announce announces e in the unit of work ctx carries, so the
	// announcement is delivered when that unit commits, in commit order, and
	// never for one that rolls back.
	Announce(ctx context.Context, e governance.Entry) error
}

// ChangeFollower holds the notices of one project a stream has not sent.
type ChangeFollower interface {
	// Changed is sent on when notices or a refetch may be waiting. It holds
	// one signal at most, so a follower slow to read it misses nothing: it
	// takes everything waiting when it gets to it.
	Changed() <-chan struct{}
	// Take returns the notices waiting, oldest first, and whether the
	// client must refetch before them.
	Take() ([]governance.ChangeNotice, bool)
	// Close stops following.
	Close()
}

// ChangeFeed offers the notices of the entries announced to this replica to
// the streams following their project.
type ChangeFeed interface {
	Follow(project workspace.ProjectID) ChangeFollower
}

// ChangeReplay reads the notices of entries a project has recorded.
type ChangeReplay interface {
	// NoticesAfter returns the notices of project's entries after the one
	// with after, oldest first, at most limit; ok is false when project
	// holds no entry with after or more than limit follow it.
	NoticesAfter(ctx context.Context, project workspace.ProjectID, after governance.EntryID, limit int) (notices []governance.ChangeNotice, ok bool, err error)
}

// StreamChanges follows a project's changes for its management change
// stream.
type StreamChanges struct {
	feed   ChangeFeed
	replay ChangeReplay
}

// NewStreamChanges returns the handler.
func NewStreamChanges(feed ChangeFeed, replay ChangeReplay) *StreamChanges {
	return &StreamChanges{feed: feed, replay: replay}
}

// Open follows project's changes for a client that heard every notice up
// to lastEventID, "" for a client that heard none. It follows before it
// reads what to replay, so no change falls between the two. A lastEventID
// that names no entry of the project, or that more than
// governance.MaxPendingNotices entries follow, is answered by a refetch
// instead.
func (h *StreamChanges) Open(ctx context.Context, project workspace.ProjectID, lastEventID string) (*ChangeStream, error) {
	s := &ChangeStream{follower: h.feed.Follow(project), first: true}
	if lastEventID == "" {
		return s, nil
	}
	after, err := id.Parse[governance.Entry](lastEventID)
	if err != nil {
		s.refetch = true
		return s, nil
	}
	notices, ok, err := h.replay.NoticesAfter(ctx, project, after, governance.MaxPendingNotices)
	if err != nil {
		s.follower.Close()
		return nil, err
	}
	s.refetch = !ok
	s.replayed = make(map[governance.EntryID]bool, len(notices))
	for _, n := range notices {
		s.replayed[n.Entry] = true
	}
	s.replay = notices
	return s, nil
}

// ChangeStream is one stream's view of its project's changes: the entries a
// resuming client missed first, then what the replica hears.
type ChangeStream struct {
	follower ChangeFollower
	// first is set until the first Take, which returns the replay.
	first   bool
	refetch bool
	replay  []governance.ChangeNotice
	// replayed are the entries replayed whose announcement may still
	// arrive, which is then not sent again.
	replayed map[governance.EntryID]bool
}

// Changed is sent on when more may be waiting.
func (s *ChangeStream) Changed() <-chan struct{} { return s.follower.Changed() }

// Take returns the notices to send, oldest first, and whether the client
// must refetch before them: the first call what a resuming client missed,
// every later one what the replica heard meanwhile.
func (s *ChangeStream) Take() ([]governance.ChangeNotice, bool) {
	if s.first {
		s.first = false
		return s.replay, s.refetch
	}
	notices, refetch := s.follower.Take()
	if len(s.replayed) == 0 {
		return notices, refetch
	}
	out := notices[:0:0]
	for _, n := range notices {
		if s.replayed[n.Entry] {
			delete(s.replayed, n.Entry)
			continue
		}
		out = append(out, n)
	}
	return out, refetch
}

// Close stops following.
func (s *ChangeStream) Close() { s.follower.Close() }
