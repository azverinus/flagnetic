package sinks_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	domain "github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/out/sinks"
)

// clickhouseServer answers the statements a sink sends as a ClickHouse would,
// from a table of replies keyed by the start of the statement, and keeps the
// requests it got.
type clickhouseServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
}

func startClickHouseServer(t *testing.T, replies map[string]func(w http.ResponseWriter)) *clickhouseServer {
	t.Helper()
	s := &clickhouseServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("a gzip body: %v", err)
				return
			}
			body = zr
		}
		b, _ := io.ReadAll(body)
		s.mu.Lock()
		s.requests, s.bodies = append(s.requests, r), append(s.bodies, string(b))
		s.mu.Unlock()
		statement := string(b)
		if q := r.URL.Query().Get("query"); q != "" {
			statement = q
		}
		for prefix, reply := range replies {
			if strings.HasPrefix(statement, prefix) {
				reply(w)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s
}

func exception(status int, code string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("X-ClickHouse-Exception-Code", code)
		w.WriteHeader(status)
		_, _ = w.Write([]byte("Code: " + code + ". DB::Exception: words that are never kept"))
	}
}

func describes(columns ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for _, c := range columns {
			_, _ = io.WriteString(w, `{"name":"`+c+`","type":"String"}`+"\n")
		}
	}
}

func clickhouseCall(url string, skipVerify, create bool, kinds ...domain.EventKind) appsinks.Call {
	return appsinks.Call{
		ProjectKey: "shop", Secrets: []string{"pw"},
		Sink: domain.Sink{Input: domain.Input{
			Kind: domain.KindClickHouse, EventKinds: kinds,
			Config: domain.Config{ClickHouse: &domain.ClickHouse{
				URL: url, Database: "db", TablePrefix: "fl", Username: "user", TLSSkipVerify: skipVerify, CreateTables: create,
				Settings: map[string]string{"max_threads": "2"},
			}},
		}},
	}
}

func newClickHouse() *sinks.ClickHouse {
	return sinks.NewClickHouse(
		sinks.NewClient(domain.KindClickHouse, sinks.Options{}),
		sinks.NewClient(domain.KindClickHouse, sinks.Options{TLSSkipVerify: true}))
}

func TestAnInsertCarriesTheDeliveryIDTheSettingsAndTheCredentialsInHeadersOnly(t *testing.T) {
	cols := []string{"schema_version", "event_id", "project_key", "environment_key", "received_at", "unit_id", "experiment_key"}
	srv := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": describes(cols...)})
	rows := domain.SampleRows(domain.EventExposure, "shop", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))[:2]

	f := newClickHouse().Deliver(t.Context(), clickhouseCall(srv.URL, true, false, domain.EventExposure),
		appsinks.Delivery{ID: "dlv_abc", Kind: domain.EventExposure, Rows: rows})
	if f != nil {
		t.Fatalf("Deliver = %v, want an acknowledgement", f)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.requests) != 2 {
		t.Fatalf("%d requests, want a DESCRIBE and an INSERT", len(srv.requests))
	}
	insert, body := srv.requests[1], srv.bodies[1]
	q := insert.URL.Query()
	for k, v := range map[string]string{
		"query_id": "dlv_abc", "async_insert": "0", "date_time_input_format": "best_effort", "max_threads": "2",
		"query": "INSERT INTO `db`.`fl_exposures` (schema_version, event_id, project_key, environment_key, experiment_key, unit_id, received_at) FORMAT JSONEachRow",
	} {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
		}
	}
	if insert.Header.Get("X-ClickHouse-User") != "user" || insert.Header.Get("X-ClickHouse-Key") != "pw" || strings.Contains(insert.URL.String(), "pw") {
		t.Errorf("credentials: user %q key %q url %s, want them in headers only", insert.Header.Get("X-ClickHouse-User"), insert.Header.Get("X-ClickHouse-Key"), insert.URL)
	}
	if lines := strings.Split(strings.TrimSpace(body), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], `{"schema_version":1,"event_id":"sample-1"`) ||
		strings.Contains(body, "variation_key") || !strings.Contains(lines[0], `"received_at":"2026-01-02T03:04:05.000Z"`) {
		t.Errorf("body %q, want two JSONEachRow lines of the columns the table has", body)
	}
}

func TestADeliveryCreatesAMissingTableOnlyWhenTheSinkMayAndAnEmptyOneCallsNothing(t *testing.T) {
	srv := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": exception(404, "60")})
	c := newClickHouse()
	rows := domain.SampleRows(domain.EventAlias, "shop", time.Now())

	f := c.Deliver(t.Context(), clickhouseCall(srv.URL, true, false, domain.EventAlias), appsinks.Delivery{ID: "dlv_1", Kind: domain.EventAlias, Rows: rows})
	if f == nil || f.Class != domain.ClassRejected || f.Code != "60 UNKNOWN_TABLE" {
		t.Errorf("a missing table without create_tables: %v, want rejected 60 UNKNOWN_TABLE", f)
	}
	if n := len(srv.requests); n != 1 {
		t.Errorf("%d requests, want only the DESCRIBE", n)
	}

	// With create_tables the table is created and the whole schema inserted.
	if f := c.Deliver(t.Context(), clickhouseCall(srv.URL, true, true, domain.EventAlias), appsinks.Delivery{ID: "dlv_2", Kind: domain.EventAlias, Rows: rows}); f != nil {
		t.Fatalf("a missing table with create_tables: %v", f)
	}
	if n := len(srv.bodies); n != 4 || !strings.HasPrefix(srv.bodies[2], "CREATE TABLE IF NOT EXISTS `db`.`fl_aliases`") {
		t.Errorf("%d requests, third %q, want DESCRIBE, CREATE, INSERT", n, srv.bodies[2])
	}

	before := len(srv.requests)
	if f := c.Deliver(t.Context(), clickhouseCall(srv.URL, true, true, domain.EventAlias), appsinks.Delivery{ID: "dlv_3", Kind: domain.EventAlias}); f != nil {
		t.Errorf("an empty batch: %v, want an acknowledgement", f)
	}
	if f := c.Deliver(t.Context(), clickhouseCall(srv.URL, true, true), appsinks.Delivery{ID: "dlv_4", Kind: "audit", Rows: rows}); f == nil || f.Class != domain.ClassRejected {
		t.Errorf("a kind with no table: %v, want a rejection", f)
	}
	if len(srv.requests) != before {
		t.Error("an empty batch or an unknown kind called ClickHouse")
	}
}

func TestAnInsertIntoATableLackingARequiredColumnIsRejectedWithoutInserting(t *testing.T) {
	srv := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": describes("schema_version", "project_key")})
	f := newClickHouse().Deliver(t.Context(), clickhouseCall(srv.URL, true, false, domain.EventAlias),
		appsinks.Delivery{ID: "dlv_1", Kind: domain.EventAlias, Rows: domain.SampleRows(domain.EventAlias, "shop", time.Now())})
	if f == nil || f.Class != domain.ClassRejected || f.Code != "16 NO_SUCH_COLUMN_IN_TABLE" || len(srv.requests) != 1 {
		t.Errorf("Deliver = %v after %d requests, want rejected 16 NO_SUCH_COLUMN_IN_TABLE after the DESCRIBE alone", f, len(srv.requests))
	}
}

func TestWhatClickHouseAnswersIsClassifiedAndNeverKept(t *testing.T) {
	rows := domain.SampleRows(domain.EventAlias, "shop", time.Now())
	all := []string{"schema_version", "event_id", "project_key", "environment_key", "received_at"}
	for name, tc := range map[string]struct {
		insert func(http.ResponseWriter)
		class  domain.FailureClass
		status int
		code   string
	}{
		"too many parts":     {exception(500, "252"), domain.ClassStatus, 500, "252 TOO_MANY_PARTS"},
		"overloaded":         {exception(429, ""), domain.ClassStatus, 429, ""},
		"cannot parse":       {exception(400, "27"), domain.ClassRejected, 400, "27 CANNOT_PARSE_INPUT_ASSERTION_FAILED"},
		"wrong password":     {exception(403, "516"), domain.ClassRejected, 403, "516 AUTHENTICATION_FAILED"},
		"an unknown failure": {exception(400, "9999"), domain.ClassRejected, 400, "9999"},
		"a redirect": {func(w http.ResponseWriter) {
			w.Header().Set("Location", "http://elsewhere.invalid/")
			w.WriteHeader(307)
		}, domain.ClassRedirect, 307, ""},
	} {
		t.Run(name, func(t *testing.T) {
			srv := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": describes(all...), "INSERT": tc.insert})
			f := newClickHouse().Deliver(t.Context(), clickhouseCall(srv.URL, true, false, domain.EventAlias), appsinks.Delivery{ID: "dlv_1", Kind: domain.EventAlias, Rows: rows})
			if f == nil || f.Class != tc.class || f.Status != tc.status || f.Code != tc.code {
				t.Errorf("Deliver = %v, want %s %d %q", f, tc.class, tc.status, tc.code)
			}
			if f != nil && strings.Contains(f.Error(), "words that are never kept") {
				t.Errorf("the failure %q holds ClickHouse's own words", f)
			}
		})
	}
}

func TestAProbeNamesWhatIsWrongWithTheTablesAndCreatesThemOnlyWhenAskedTo(t *testing.T) {
	srv := startClickHouseServer(t, map[string]func(http.ResponseWriter){
		"DESCRIBE TABLE `db`.`fl_exposures`": describes("schema_version", "event_id", "project_key", "environment_key", "received_at"),
		"DESCRIBE TABLE `db`.`fl_aliases`":   exception(404, "60"),
	})
	c := newClickHouse()
	call := clickhouseCall(srv.URL, true, true, domain.EventExposure, domain.EventAlias)

	got := c.Probe(t.Context(), call, time.Now(), false)
	if got.Failure != nil || len(got.Warnings) == 0 || !strings.Contains(strings.Join(got.Warnings, "|"), "table `db`.`fl_aliases` does not exist") ||
		!strings.Contains(strings.Join(got.Warnings, "|"), "column experiment_key is absent from table `db`.`fl_exposures`; inserts skip it") {
		t.Fatalf("an unprepared probe = %+v, want warnings that the alias table would be created and which columns are absent", got)
	}
	for _, body := range srv.bodies {
		if strings.HasPrefix(body, "CREATE") {
			t.Fatalf("an unprepared probe created a table: %s", body)
		}
	}
	if got := c.Probe(t.Context(), call, time.Now(), true); got.Failure != nil {
		t.Fatalf("a prepared probe = %+v", got)
	}
	created := 0
	for _, body := range srv.bodies {
		if strings.HasPrefix(body, "CREATE TABLE IF NOT EXISTS `db`.`fl_aliases`") {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d tables created, want the missing alias table", created)
	}
}

func TestAProbeFailsOnAnAnswerFromAServerThatIsDownOrDoesNotTrustThePlatform(t *testing.T) {
	srv := startClickHouseServer(t, map[string]func(http.ResponseWriter){"SELECT 1": exception(503, "")})
	if got := newClickHouse().Probe(t.Context(), clickhouseCall(srv.URL, true, false, domain.EventAlias), time.Now(), true); got.Failure == nil ||
		got.Failure.Class != domain.ClassStatus || got.Failure.Status != 503 {
		t.Errorf("a probe of a busy server = %+v, want class status 503", got)
	}
	// A certificate nobody the platform trusts signed is a TLS failure unless
	// the sink says not to verify.
	if got := newClickHouse().Probe(t.Context(), clickhouseCall(srv.URL, false, false, domain.EventAlias), time.Now(), true); got.Failure == nil || got.Failure.Class != domain.ClassTLS {
		t.Errorf("a probe verifying an untrusted certificate = %+v, want class tls", got)
	}
	// A DESCRIBE that fails for a reason other than a missing table fails the probe.
	broken := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": exception(404, "81")})
	if got := newClickHouse().Probe(t.Context(), clickhouseCall(broken.URL, true, false, domain.EventAlias), time.Now(), true); got.Failure == nil ||
		got.Failure.Code != "81 UNKNOWN_DATABASE" {
		t.Errorf("a probe of a database that does not exist = %+v, want 81 UNKNOWN_DATABASE", got)
	}
	// A DESCRIBE answer that is not JSON is the server's mistake, not a table's.
	garbled := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": func(w http.ResponseWriter) { _, _ = io.WriteString(w, "not json\n") }})
	if got := newClickHouse().Probe(t.Context(), clickhouseCall(garbled.URL, true, false, domain.EventAlias), time.Now(), true); got.Failure == nil {
		t.Errorf("a probe of a server whose DESCRIBE is not JSON = %+v, want a failure", got)
	}
}

func TestTheTargetsAnswerForEveryKindThatCanBeDeliveredTo(t *testing.T) {
	targets := sinks.NewTargets(governance.TargetPolicy{}, nil)
	for _, k := range []domain.Kind{domain.KindWebhook, domain.KindClickHouse} {
		if _, ok := targets.For(k); !ok {
			t.Errorf("no target for %s", k)
		}
	}
	if _, ok := targets.For(domain.KindObjectStorage); ok {
		t.Error("a target for object storage before it is built")
	}
}

func TestAProbeFailsWhenATableCannotBeCreatedAndAnInsertWhenItsTableCannotBeDescribed(t *testing.T) {
	srv := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": exception(404, "60"), "CREATE": exception(403, "497")})
	c := newClickHouse()
	if got := c.Probe(t.Context(), clickhouseCall(srv.URL, true, true, domain.EventAlias), time.Now(), true); got.Failure == nil ||
		got.Failure.Code != "497 ACCESS_DENIED" {
		t.Errorf("a probe that may not create = %+v, want 497 ACCESS_DENIED", got)
	}
	rows := domain.SampleRows(domain.EventAlias, "shop", time.Now())
	if f := c.Deliver(t.Context(), clickhouseCall(srv.URL, true, true, domain.EventAlias), appsinks.Delivery{ID: "dlv_1", Kind: domain.EventAlias, Rows: rows}); f == nil ||
		f.Code != "497 ACCESS_DENIED" {
		t.Errorf("a delivery whose table cannot be created = %v, want 497 ACCESS_DENIED", f)
	}
	down := startClickHouseServer(t, map[string]func(http.ResponseWriter){"DESCRIBE": exception(503, "")})
	if f := c.Deliver(t.Context(), clickhouseCall(down.URL, true, true, domain.EventAlias), appsinks.Delivery{ID: "dlv_1", Kind: domain.EventAlias, Rows: rows}); f == nil ||
		f.Class != domain.ClassStatus {
		t.Errorf("a delivery whose table cannot be described = %v, want class status", f)
	}
}
