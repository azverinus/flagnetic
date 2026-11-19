package access_test

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Closing local login counts only the sessions it ends: one its idle
// timeout ended already signs nobody in, and is left to the retention worker.
func TestRevokingRefusedSessionsCountsOnlyTheLiveOnes(t *testing.T) {
	pool := testkit.FreshPool(t)
	store := pgaccess.NewStore(pool)
	m := testkit.NewMember(t, pool, testkit.WithEmail("ann@example.com"))
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	timeouts := access.DefaultAuthSettings.SessionTimeouts()
	ids := id.NewSource(testkit.Clock())
	open := func(at time.Time) access.Session {
		t.Helper()
		s, _ := access.NewSession(id.New[access.Session](ids), access.NewSessionInput{
			Member: m.ID, Method: access.SignInLocal, Timeouts: timeouts, Now: at,
		}, rand.Reader, bytes.Repeat([]byte{1}, 32))
		if err := store.CreateSession(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	live := open(now.Add(-time.Minute))
	idle := open(now.Add(-timeouts.Idle - time.Minute))

	closed := access.DefaultAuthSettings
	closed.LocalLoginEnabled = false
	got, err := store.RevokeRefusedSessions(t.Context(), closed, now)
	if err != nil || got != 1 {
		t.Fatalf("revoked = %d, %v; want the one live session", got, err)
	}
	unrevoked, err := store.MemberSessions(t.Context(), m.ID)
	if err != nil || len(unrevoked) != 1 || unrevoked[0].ID != idle.ID || unrevoked[0].ID == live.ID {
		t.Errorf("unrevoked sessions = %+v, %v; want only the idle one", unrevoked, err)
	}
}

// The purge states in SQL which sessions have ended; the domain states the
// same as cutoffs. They agree at every edge: a session exactly at a cutoff
// has ended, one an instant after it has not.
func TestPurgingSessionsEndsExactlyThoseTheCutoffsEnd(t *testing.T) {
	pool := testkit.FreshPool(t)
	store := pgaccess.NewStore(pool)
	m := testkit.NewMember(t, pool)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	timeouts := access.DefaultAuthSettings.SessionTimeouts()
	cut := access.EndedSessionCutoffs(now, timeouts)
	ids := id.NewSource(testkit.Clock())
	revoked := now

	type edge struct {
		name string
		s    access.Session
	}
	base := func() access.Session {
		return access.Session{
			ID: id.New[access.Session](ids), Member: m.ID, Method: access.SignInLocal,
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		}
	}
	var edges []edge
	for _, d := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
		s := base()
		s.LastSeenAt = cut.LastSeenBy.Add(d)
		edges = append(edges, edge{"last request at the idle cutoff " + d.String(), s})
		s = base()
		s.CreatedAt = cut.CreatedBy.Add(d)
		edges = append(edges, edge{"opened at the absolute cutoff " + d.String(), s})
		s = base()
		s.ExpiresAt = cut.ExpiresBy.Add(d)
		edges = append(edges, edge{"stored expiry at its cutoff " + d.String(), s})
	}
	s := base()
	s.RevokedAt = &revoked
	edges = append(edges, edge{"revoked", s}, edge{"live", base()})

	for i := range edges {
		secret := bytes.Repeat([]byte{byte(i + 1)}, 32)
		edges[i].s.Hash = access.SessionHash(secret)
		if err := store.CreateSession(t.Context(), edges[i].s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PurgeSessions(t.Context(), cut, len(edges)+1); err != nil {
		t.Fatal(err)
	}
	left, err := store.MemberSessions(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[access.SessionID]bool{}
	for _, s := range left {
		kept[s.ID] = true
	}
	for _, e := range edges {
		if ended := !kept[e.s.ID]; ended != cut.Ended(e.s) {
			t.Errorf("%s: purged = %v, the cutoffs say ended = %v", e.name, ended, cut.Ended(e.s))
		}
	}
}
