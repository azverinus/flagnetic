package management

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
)

// exhaustedPoolWaiting stands for a database whose connection pool is
// exhausted when the answer counts the change requests waiting: no real
// database fails there on demand. It overrides the one read and answers
// the error.
type exhaustedPoolWaiting struct{}

func (exhaustedPoolWaiting) WaitingOn(context.Context, flags.FlagID, workspace.EnvironmentID) (governance.Waiting, error) {
	return governance.Waiting{}, errors.New("pool exhausted")
}

// timedOutSettings stands for a database whose query for the authentication
// settings times out: no real database fails there on demand. It overrides
// the one read and answers the error.
type timedOutSettings struct{}

func (timedOutSettings) AuthSettings(context.Context) (access.AuthSettings, error) {
	return access.AuthSettings{}, errors.New("query timed out")
}

// A write that committed is answered with what it made even when what the
// answer adds cannot be read: failing there would answer 500 for a write that
// is done, and a retry would meet a version no longer current, or a secret
// already consumed.
func TestAWriteThatCommittedIsAnsweredWhenWhatTheAnswerAddsCannotBeRead(t *testing.T) {
	var logged bytes.Buffer
	edge := flaghttp.New(flaghttp.Options{Logger: slog.New(slog.NewTextHandler(&logged, nil))})
	a := newAPI(edge, Handlers{
		CountWaiting:    appgovernance.NewCountWaitingChangeRequests(exhaustedPoolWaiting{}),
		GetAuthSettings: appaccess.NewGetAuthSettings(timedOutSettings{}),
	}, newRefusals(prometheus.NewRegistry()), Options{})

	ec := flags.EnvironmentConfiguration{Configuration: flags.Configuration{Version: 7, On: true}}
	got := a.writtenConfiguration(t.Context(), ec)
	if got.Version != 7 || !got.On || got.ChangeRequests != nil {
		t.Errorf("configuration = %+v, want it answered without the waiting change requests", got)
	}

	member := access.Member{Email: "ann@example.com"}
	me := a.meAfterWrite(t.Context(), access.Caller{Method: access.MethodSession, Member: &member})
	if me.Member == nil || me.Member.Email != member.Email || me.MaxTokenLifetimeSeconds != nil {
		t.Errorf("me = %+v, want the member without the token policy", me)
	}

	for _, want := range []string{"pool exhausted", "query timed out"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log lacks %q: %s", want, logged.String())
		}
	}
	// Reading is another matter: a read that cannot be answered says so.
	if _, err := a.configurationWithWaiting(t.Context(), ec); err == nil {
		t.Error("a read answered without its waiting change requests, want the failure")
	}
	if _, err := a.meOf(t.Context(), access.Caller{}); err == nil {
		t.Error("GET /me answered without the auth settings, want the failure")
	}
}
