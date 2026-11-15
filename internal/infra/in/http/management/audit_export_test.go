package management_test

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// member returns a signed-in member holding role in the fixture's mobile
// project.
func (l auditLog) member(t *testing.T, role access.RoleName) *testkit.Browser {
	t.Helper()
	m := testkit.NewMember(t, l.pool)
	testkit.Grant(t, l.pool, projectIDOf(l.mobile), m.ID, role)
	return signedIn(t, l.srv, m)
}

func sameDocument(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %s", a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %s", b)
	}
	return reflect.DeepEqual(x, y)
}

func TestTheProjectAuditLogDownloadsAsJSONLinesEachLineTheEntryTheAPIReads(t *testing.T) {
	l := writeAuditLog(t)
	viewer := l.member(t, access.RoleViewer)
	ctx := t.Context()

	res := do(viewer.ExportProjectAuditEntriesWithResponse(ctx, "mobile", &management.ExportProjectAuditEntriesParams{}))
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("export: status = %d, body %s", res.StatusCode(), res.Body)
	}
	if ct := res.HTTPResponse.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}
	name := regexp.MustCompile(`^attachment; filename="audit-mobile-` + l.clock.Now().UTC().Format("20060102T150405Z") + `\.jsonl"$`)
	if cd := res.HTTPResponse.Header.Get("Content-Disposition"); !name.MatchString(cd) {
		t.Errorf("Content-Disposition = %q", cd)
	}

	want := actions(t, "list", do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{})))
	var got []string
	scanner := bufio.NewScanner(bytes.NewReader(res.Body))
	for scanner.Scan() {
		var entry management.AuditEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("line %q: %v", scanner.Bytes(), err)
		}
		got = append(got, entry.Action)
		one := do(l.admin.GetProjectAuditEntryWithResponse(ctx, "mobile", entry.Id.String()))
		if !sameDocument(t, scanner.Bytes(), one.Body) {
			t.Errorf("line\n got %s\nwant %s", scanner.Bytes(), one.Body)
		}
	}
	if !slices.Equal(got, want) || len(got) != 5 {
		t.Errorf("exported actions = %v, want the log's %v", got, want)
	}
}

func TestADownloadAppliesTheFiltersOfTheList(t *testing.T) {
	l := writeAuditLog(t)
	res := do(l.admin.ExportProjectAuditEntriesWithResponse(t.Context(), "mobile", &management.ExportProjectAuditEntriesParams{
		EntityKind: ptr("environment"), Q: ptr("qa"),
	}))
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("export: status = %d, body %s", res.StatusCode(), res.Body)
	}
	lines := strings.Split(strings.TrimSpace(string(res.Body)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"environment.updated"`) || !strings.Contains(lines[1], `"environment.created"`) {
		t.Errorf("export = %s, want the two entries of the qa environment", res.Body)
	}
}

func TestTheProjectAuditLogDownloadsAsCSV(t *testing.T) {
	l := writeAuditLog(t)
	ctx := t.Context()
	format := management.ExportProjectAuditEntriesParamsFormatCsv

	res := do(l.admin.ExportProjectAuditEntriesWithResponse(ctx, "mobile", &management.ExportProjectAuditEntriesParams{Format: &format}))
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("export: status = %d, body %s", res.StatusCode(), res.Body)
	}
	if ct := res.HTTPResponse.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := res.HTTPResponse.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="audit-mobile-`) || !strings.HasSuffix(cd, `.csv"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	records, err := csv.NewReader(bytes.NewReader(res.Body)).ReadAll()
	if err != nil {
		t.Fatalf("parse %s: %v", res.Body, err)
	}
	if !slices.Equal(records[0], governance.CSVHeader) {
		t.Fatalf("header = %v, want %v", records[0], governance.CSVHeader)
	}
	list := do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{})).JSON200.Items
	rows := records[1:]
	if len(rows) != len(list) {
		t.Fatalf("%d rows for %d entries", len(rows), len(list))
	}
	column := func(row []string, name string) string { return row[slices.Index(governance.CSVHeader, name)] }
	for i, row := range rows {
		e := list[i]
		if column(row, "id") != e.Id.String() || column(row, "action") != e.Action || column(row, "entity_key") != e.Entity.Key ||
			column(row, "actor_kind") != string(e.Actor.Kind) || column(row, "project_id") != l.mobile.Id.String() {
			t.Errorf("row %d = %q, want entry %s %s", i, row, e.Id, e.Action)
		}
		if e.After != nil {
			after, _ := json.Marshal(e.After)
			if !sameDocument(t, []byte(column(row, "after")), after) {
				t.Errorf("row %d after = %s, want %s", i, column(row, "after"), after)
			}
		}
	}
	if updated := rows[2]; column(updated, "action") != "environment.updated" || column(updated, "before") == "" || column(updated, "after") == "" {
		t.Errorf("environment.updated row = %q, want its before and after", updated)
	}
	if created := rows[len(rows)-1]; column(created, "action") != "project.created" || column(created, "before") != "" || column(created, "environment_id") != "" {
		t.Errorf("project.created row = %q, want an empty before and environment", created)
	}
}

func TestACSVDownloadShowsAFormulaTypedAsANameAsText(t *testing.T) {
	l := writeAuditLog(t)
	ctx := t.Context()
	const formula = `=HYPERLINK("https://evil.example","see logs")`
	m := testkit.NewMember(t, l.pool, testkit.WithDisplayName(formula))
	testkit.Grant(t, l.pool, projectIDOf(l.mobile), m.ID, access.RoleMaintainer)
	created := do(signedIn(t, l.srv, m).CreateUnitTypeWithResponse(ctx, "mobile", management.UnitTypeCreate{Key: "account", Name: "Account"}))
	if created.StatusCode() != http.StatusCreated {
		t.Fatalf("create unit type: status = %d, body %s", created.StatusCode(), created.Body)
	}

	format := management.ExportProjectAuditEntriesParamsFormatCsv
	res := do(l.admin.ExportProjectAuditEntriesWithResponse(ctx, "mobile", &management.ExportProjectAuditEntriesParams{
		Format: &format, Q: ptr("account"),
	}))
	records, err := csv.NewReader(bytes.NewReader(res.Body)).ReadAll()
	if err != nil || len(records) != 2 {
		t.Fatalf("export: status = %d, body %s, err %v", res.StatusCode(), res.Body, err)
	}
	if got := records[1][slices.Index(governance.CSVHeader, "actor_name")]; got != "'"+formula {
		t.Errorf("actor_name = %q, want the formula prefixed with a quote", got)
	}
}

func TestADownloadOfAnUnknownFormatIsRefused(t *testing.T) {
	l := writeAuditLog(t)
	for _, path := range []string{"/api/v1/projects/mobile/audit/export?format=xml", "/api/v1/audit/export?format=xml"} {
		res := request(t, http.MethodGet, l.srv.URL+path, l.adminToken)
		assertProblem(t, path, res.status, res.body, http.StatusBadRequest, "invalid_request")
	}
}

func TestADownloadAboveTheCapIsRefusedBeforeAnythingIsSent(t *testing.T) {
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, testkit.WithPool(pool), testkit.WithAuditExportMaxEntries(3))
	admin := testkit.Client(t, srv, testkit.Bootstrap(t, pool))
	p := createProjectAsCreated(t, admin, "mobile")
	for _, key := range []string{"qa", "dev", "uat"} {
		do(admin.CreateEnvironmentWithResponse(t.Context(), p.Key, management.EnvironmentCreate{Key: key, Name: key}))
	}

	res := do(admin.ExportProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ExportProjectAuditEntriesParams{}))
	problem := assertProblem(t, "four entries under a cap of three", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "export_too_large")
	if problem.Detail == nil || !strings.Contains(*problem.Detail, "3") {
		t.Errorf("refusal %v does not name the cap", problem.Detail)
	}
	if cd := res.HTTPResponse.Header.Get("Content-Disposition"); cd != "" {
		t.Errorf("Content-Disposition = %q on a refusal", cd)
	}
	within := do(admin.ExportProjectAuditEntriesWithResponse(t.Context(), p.Key, &management.ExportProjectAuditEntriesParams{EntityKind: ptr("environment")}))
	if within.StatusCode() != http.StatusOK || bytes.Count(within.Body, []byte("\n")) != 3 {
		t.Errorf("exactly the cap: status = %d, body %s", within.StatusCode(), within.Body)
	}
	instance := do(admin.ExportAuditEntriesWithResponse(t.Context(), &management.ExportAuditEntriesParams{}))
	assertProblem(t, "the instance log over the cap", instance.StatusCode(), instance.Body, http.StatusUnprocessableEntity, "export_too_large")
}

func TestTheInstanceAuditLogDownloadsForAnInstanceAdminAlone(t *testing.T) {
	l := writeAuditLog(t)
	ctx := t.Context()

	owner := l.member(t, access.RoleOwner)
	refused := do(owner.ExportAuditEntriesWithResponse(ctx, &management.ExportAuditEntriesParams{}))
	assertProblem(t, "a project owner", refused.StatusCode(), refused.Body, http.StatusForbidden, "forbidden")

	res := do(l.admin.ExportAuditEntriesWithResponse(ctx, &management.ExportAuditEntriesParams{Project: &l.mobile.Id}))
	if res.StatusCode() != http.StatusOK {
		t.Fatalf("instance export: status = %d, body %s", res.StatusCode(), res.Body)
	}
	if cd := res.HTTPResponse.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="audit-instance-`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if n := bytes.Count(res.Body, []byte("\n")); n != 5 {
		t.Errorf("one project's export holds %d lines, want 5", n)
	}
	all := do(l.admin.ExportAuditEntriesWithResponse(ctx, &management.ExportAuditEntriesParams{}))
	instance := do(l.admin.ListAuditEntriesWithResponse(ctx, &management.ListAuditEntriesParams{Limit: ptr(200)})).JSON200.Items
	if n := bytes.Count(all.Body, []byte("\n")); n != len(instance) || n <= 5 {
		t.Errorf("the instance's export holds %d lines, want the %d entries of the instance, those of no project too", n, len(instance))
	}
}

// seededDownload is an instance download of an audit log of a few thousand
// large entries, which fills the buffers between server and client many
// times over, so a client that stops reading stalls the server's writes.
type seededDownload struct {
	srv   *testkit.Server
	pool  *pgxpool.Pool
	token string
}

func seedDownload(t *testing.T, opts ...testkit.ServerOption) seededDownload {
	t.Helper()
	pool := testkit.FreshPool(t)
	srv := testkit.StartServer(t, append([]testkit.ServerOption{testkit.WithPool(pool)}, opts...)...)
	token := testkit.Bootstrap(t, pool)
	testkit.SeedAuditEntries(t, pool, 3000, 20_000)
	return seededDownload{srv: srv, pool: pool, token: token}
}

// stalled starts the instance download and reads only the head of its
// answer, leaving the body unread behind a receive buffer too small to hold
// any of it.
func (d seededDownload) stalled(t *testing.T) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(d.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.(*net.TCPConn).SetReadBuffer(1024)
	_, _ = io.WriteString(conn, "GET /api/v1/audit/export HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer "+d.token+"\r\n\r\n")
	br := bufio.NewReaderSize(conn, 256)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("download head = %q, %v", status, err)
	}
	return conn
}

func TestADownloadToAClientThatStopsReadingEndsAndReleasesItsConnection(t *testing.T) {
	d := seedDownload(t, testkit.WithEdge(func(o *flaghttp.Options) { o.WriteTimeout = 300 * time.Millisecond }))
	d.stalled(t)

	deadline := time.Now().Add(10 * time.Second)
	for len(d.srv.Logs.Find("audit download cut short")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the download was still held ten seconds after its client stopped reading")
		}
		if n := d.pool.Stat().AcquiredConns(); n != 0 {
			t.Fatalf("%d connections held while the download waited on its client", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := d.pool.Stat().AcquiredConns(); n != 0 {
		t.Errorf("%d connections held after the download ended", n)
	}
}

func TestADownloadSpanningSeveralPagesHoldsEveryEntryOnceNewestFirst(t *testing.T) {
	d := seedDownload(t, testkit.WithEdge(func(o *flaghttp.Options) { o.WriteTimeout = 2 * time.Second }))
	res := request(t, http.MethodGet, d.srv.URL+"/api/v1/audit/export", d.token)
	if res.status != http.StatusOK {
		t.Fatalf("status = %d", res.status)
	}
	// 3000 seeded entries and those the bootstrap wrote, across pages of a
	// thousand.
	if n := bytes.Count(res.body, []byte("\n")); n < 3000 {
		t.Errorf("the download holds %d lines, want the 3000 seeded and more", n)
	}
	ids := map[string]bool{}
	var last string
	for line := range bytes.Lines(res.body) {
		var e management.AuditEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("line: %v", err)
		}
		id := e.Id.String()
		if ids[id] {
			t.Fatalf("entry %s appears twice across pages", id)
		}
		if ids[id] = true; last != "" && id > last {
			t.Fatalf("entry %s after %s: not newest first", id, last)
		}
		last = id
	}
}

func TestDownloadsBeyondTheCapRunningAtOnceAreRefusedUntilOneEnds(t *testing.T) {
	d := seedDownload(t, testkit.WithAuditExportMaxConcurrent(1))
	held := d.stalled(t)

	res := request(t, http.MethodGet, d.srv.URL+"/api/v1/audit/export", d.token)
	assertProblem(t, "a second download", res.status, res.body, http.StatusTooManyRequests, "too_many_exports")

	_ = held.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		res = request(t, http.MethodGet, d.srv.URL+"/api/v1/audit/export", d.token)
		if res.status == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slot was not freed ten seconds after its client left: %d", res.status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
