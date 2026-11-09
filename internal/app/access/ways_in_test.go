package access_test

import (
	"context"
	"errors"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestOnlyASessionRemovesAPassword(t *testing.T) {
	h := appaccess.NewRemoveMyPassword(nil, nil, nil, nil, nil, nil)
	member := access.Member{ID: access.MemberID{}}
	for name, caller := range map[string]access.Caller{
		"no member":                  {},
		"a member without a session": {Member: &member},
	} {
		t.Run(name, func(t *testing.T) {
			err := h.Handle(t.Context(), appaccess.RemoveMyPasswordCommand{Caller: caller})
			if !errors.Is(err, access.ErrSessionRequired) {
				t.Errorf("removing a password without a session = %v, want session_required", err)
			}
		})
	}
}

func TestAWayInThatCannotBeToldIsNotRemoved(t *testing.T) {
	pool := testkit.Pool(t)
	m := testkit.NewMember(t, pool)
	ways := appaccess.NewWaysIn(pgaccess.NewStore(pool), nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	member := access.Member{ID: m.ID, Email: m.Email, Status: access.MemberActive}
	if err := ways.EnsureWayIn(ctx, member, true, ""); err == nil || errors.Is(err, access.ErrLastAuthMethod) {
		t.Errorf("EnsureWayIn with the store unreachable = %v, want the store's error", err)
	}
}
