package access_test

import (
	"errors"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
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
