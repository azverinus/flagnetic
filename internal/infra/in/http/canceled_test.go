package http_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"testing"
	"time"

	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// slowQuery mounts a route that runs a query in PostgreSQL lasting far longer
// than any test, under the context ctxOf derives from the request's, and
// answers the query's error. started is closed once the handler is about to
// query.
func slowQuery(t *testing.T, pattern string, started chan<- struct{}, ctxOf func(*nethttp.Request) (context.Context, context.CancelFunc)) testkit.ServerOption {
	pool := testkit.Pool(t)
	return testkit.WithRoute(pattern, func(srv *flaghttp.Server) nethttp.Handler {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			ctx, cancel := ctxOf(r)
			defer cancel()
			if started != nil {
				close(started)
			}
			_, err := pool.Exec(ctx, "SELECT pg_sleep(60)")
			if err == nil {
				err = errors.New("the query ended without being cancelled")
			}
			srv.WriteError(w, r.WithContext(ctx), fmt.Errorf("listing flags: %w", err))
		})
	})
}

func requestContext(r *nethttp.Request) (context.Context, context.CancelFunc) {
	return r.Context(), func() {}
}

// accessRecord waits for the access log record of route.
func accessRecord(t *testing.T, srv *testkit.Server, route string) testkit.LogRecord {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range srv.Logs.Find("request") {
			if r.Attrs["route"] == route {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no access log record of %s", route)
	return testkit.LogRecord{}
}

func TestARequestTheClientCanceledIsNotAFailure(t *testing.T) {
	const route = "GET /test/slow"
	started := make(chan struct{})
	srv := testkit.StartServer(t, slowQuery(t, route, started, requestContext))

	ctx, cancel := context.WithCancel(t.Context())
	req, err := nethttp.NewRequestWithContext(ctx, "GET", srv.URL+"/test/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	answered := make(chan error, 1)
	go func() {
		resp, err := nethttp.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		answered <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler never started its query")
	}
	// Usually long enough for the query to be running in PostgreSQL, so the
	// cancellation interrupts it there. Nothing guarantees it: on a slow
	// machine the driver may see the cancellation before sending the query
	// and refuse it, which ends the request with the same error.
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-answered:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the client got %v, want its own cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the client's request did not end after it was canceled")
	}

	logged := accessRecord(t, srv, route)
	if logged.Level != slog.LevelInfo || logged.Attrs["status"] != int64(499) {
		t.Errorf("access log record %v at %v, want status 499 at info", logged.Attrs, logged.Level)
	}
	if d, _ := logged.Attrs["duration"].(time.Duration); d >= 30*time.Second {
		t.Errorf("the request took %v: the query outlived its client", d)
	}
	if failed := srv.Logs.Find("request failed"); len(failed) != 0 {
		t.Errorf("a request the client canceled was logged as a failure: %v", failed)
	}
	canceled := srv.Logs.Find("request canceled by the client")
	if len(canceled) != 1 || canceled[0].Level != slog.LevelDebug {
		t.Errorf("logged %v, want one debug record of the cancellation", canceled)
	}
	scrape := srv.Scrape(t)
	if got := metric(t, scrape, `flagnetic_http_requests_total{code="499",method="GET",route="GET /test/slow"}`); got != 1 {
		t.Errorf("requests counted as 499 = %v, want 1:\n%s", got, grep(scrape, "flagnetic_http_requests_total"))
	}
	if got := metric(t, scrape, `flagnetic_http_requests_total{code="500",method="GET",route="GET /test/slow"}`); got != -1 {
		t.Errorf("a request the client canceled was counted as 500 (%v)", got)
	}
}

// failure asserts that the request to path was answered 500, logged at error
// and counted as 500.
func failure(t *testing.T, srv *testkit.Server, route, path string) {
	t.Helper()
	checkProblem(t, request(t, "GET", srv.URL+path, nil), 500, "internal", "Internal error")
	failed := srv.Logs.Find("request failed")
	if len(failed) != 1 || failed[0].Level != slog.LevelError || failed[0].Attrs["status"] != int64(500) {
		t.Errorf("logged %v, want one error record with status 500", failed)
	}
	if got := metric(t, srv.Scrape(t), `flagnetic_http_requests_total{code="500",method="GET",route="`+route+`"}`); got != 1 {
		t.Errorf("requests counted as 500 = %v, want 1", got)
	}
}

func TestAServerDeadlineIsStillAFailure(t *testing.T) {
	const route = "GET /test/deadline"
	srv := testkit.StartServer(t, slowQuery(t, route, nil, func(r *nethttp.Request) (context.Context, context.CancelFunc) {
		return context.WithTimeout(r.Context(), 100*time.Millisecond)
	}))
	failure(t, srv, route, "/test/deadline")
}

func TestARequestTheServerCanceledIsStillAFailure(t *testing.T) {
	const route = "GET /test/stopping"
	srv := testkit.StartServer(t, slowQuery(t, route, nil, func(r *nethttp.Request) (context.Context, context.CancelFunc) {
		// The replica ends the requests still running when it stops, with
		// a cause of its own: the client is still there.
		ctx, cancel := context.WithCancelCause(r.Context())
		cancel(errors.New("the replica is stopping"))
		return ctx, func() {}
	}))
	failure(t, srv, route, "/test/stopping")
}

func TestAHandlersOwnCancellationIsStillAFailure(t *testing.T) {
	const route = "GET /test/abandoned"
	srv := testkit.StartServer(t, slowQuery(t, route, nil, func(r *nethttp.Request) (context.Context, context.CancelFunc) {
		// A handler that gives up on work of its own, with a plain cancel
		// of a context it derived: the client is still there.
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		return ctx, cancel
	}))
	failure(t, srv, route, "/test/abandoned")
}
