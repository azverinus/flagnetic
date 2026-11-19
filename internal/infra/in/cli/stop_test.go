package cli_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// silentSMTP accepts connections and never answers, as a server that has hung
// does: the mail sent to it waits for a greeting that does not come. It counts
// the connections it took.
func silentSMTP(t *testing.T) (addr net.Addr, connections *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connections = new(atomic.Int32)
	var held []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			held = append(held, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		for _, c := range held {
			_ = c.Close()
		}
	})
	return ln.Addr(), connections
}

func TestAReplicaStoppedWithAMailStuckOnASilentServerStillWritesItsCountsWithinTheStopBudget(t *testing.T) {
	url := testkit.EmptyDatabase(t)
	r := serve(t, map[string]string{
		"DATABASE_URL":             url,
		"FLAGNETIC_BASE_URL":       "https://flags.example.com",
		"FLAGNETIC_SHUTDOWN_DRAIN": "0",
		"FLAGNETIC_SHUTDOWN_GRACE": "1s",
	})
	b := bootstrap(map[string]string{"DATABASE_URL": url}, "--email", "first@example.com")
	if b.code != 0 {
		t.Fatalf("bootstrap: exit code = %d; stderr:\n%s", b.code, b.stderr)
	}
	token := strings.TrimSpace(b.stdout)
	admin, err := management.NewClientWithResponses(r.baseURL+management.BaseURL, management.WithRequestEditorFn(
		func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	proj := management.ProjectCreate{Key: "shop", Name: "Shop"}
	if res, perr := admin.CreateProjectWithResponse(t.Context(), proj); perr != nil || res.JSON201 == nil {
		t.Fatalf("create project: %v %v", perr, res)
	}
	if res, ferr := admin.CreateFlagWithResponse(t.Context(), "shop", management.FlagCreate{Key: "checkout", Name: "Checkout", Type: "boolean"}); ferr != nil || res.JSON201 == nil {
		t.Fatalf("create flag: %v %v", ferr, res)
	}
	key, err := admin.CreateSdkKeyWithResponse(t.Context(), "shop", "production", management.SdkKeyCreate{Kind: string(management.SdkKeyKindClient), Name: "web"})
	if err != nil || key.JSON201 == nil {
		t.Fatalf("create SDK key: %v %v", err, key)
	}
	environment, err := admin.GetEnvironmentWithResponse(t.Context(), "shop", "production")
	if err != nil || environment.JSON200 == nil {
		t.Fatalf("get environment: %v %v", err, environment)
	}

	smtp, connections := silentSMTP(t)
	host, port, _ := net.SplitHostPort(smtp.String())
	portNumber, _ := net.LookupPort("tcp", port)
	if res, merr := admin.UpdateMailSettingsWithResponse(t.Context(), management.SmtpSettingsUpdate{
		Host: host, Port: portNumber, Security: management.SmtpSecurityNone, From: "flags@example.com",
	}); merr != nil || res.JSON200 == nil {
		t.Fatalf("configure SMTP: %v %v", merr, res)
	}

	// A remote evaluation counted in memory, and a password reset whose mail
	// waits on the server that never answers.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, r.baseURL+sdk.BaseURL+"/evaluate",
		bytes.NewReader([]byte(`{"context":{"units":{"user":"u-1"}},"flags":["checkout"]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key.JSON201.Value)
	if res, eerr := http.DefaultClient.Do(req); eerr != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("evaluate: %v %v", eerr, res)
	}
	anon, _ := management.NewClientWithResponses(r.baseURL + management.BaseURL)
	if res, rerr := anon.RequestPasswordResetWithResponse(t.Context(), management.PasswordResetRequest{Email: "first@example.com"}); rerr != nil || res.StatusCode() != http.StatusAccepted {
		t.Fatalf("password reset: %v %v", rerr, res)
	}
	if !waitFor(func() bool { return connections.Load() > 0 }) {
		t.Fatalf("the mail never reached the SMTP server:\n%s", r.stderr.String())
	}

	// The documented budget after the grace period (docs/operations.md,
	// "Stopping"): the mail would otherwise hold the stop for its 30 s
	// exchange, past the pod's termination grace period.
	const grace, cancelBound = time.Second, 10 * time.Second
	start := time.Now()
	r.signal()
	code, took := r.wait(start, grace+cancelBound+20*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, r.stderr.String())
	}
	if limit := grace + cancelBound + time.Second; took > limit {
		t.Errorf("the replica stopped %v after the signal, want within the grace period and the cancel step, %v:\n%s", took, limit, r.stderr.String())
	}

	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rows, err := pgevents.NewStore(pool).EvaluationSummaries(t.Context(), id.FromBytes[workspace.Environment](environment.JSON200.Id))
	if err != nil {
		t.Fatal(err)
	}
	var counted int
	for _, row := range rows {
		if row.Source == delivery.SourceRemoteEvaluation && row.Row.Flag == "checkout" {
			counted += int(row.Row.Count)
		}
	}
	if counted != 1 {
		t.Errorf("remote evaluations written by the stopping replica = %d, want 1 (flushed before waiting on the mail):\n%s", counted, r.stderr.String())
	}
}
