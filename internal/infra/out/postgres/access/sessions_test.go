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
