package management_test

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// auditLog is an instance whose audit log the fixture wrote: an instance
// admin acting through two tokens, and a maintainer's project token, in two
// projects, each change a minute after the one before.
type auditLog struct {
	srv                       *testkit.Server
	pool                      *pgxpool.Pool
	clock                     *clock.Fake
	admin, second, pipeline   *management.ClientWithResponses
	adminToken, secondToken   string
	adminID                   openapi_types.UUID
	mobile, web               management.Project
	qa                        management.Environment
	firstChange, lastInMobile time.Time
}

func writeAuditLog(t *testing.T) auditLog {
	t.Helper()
	pool := testkit.FreshPool(t)
	c := clock.NewFake(testkit.Start.Add(time.Hour))
	l := auditLog{srv: testkit.StartServer(t, testkit.WithPool(pool), testkit.WithClock(c)), pool: pool, clock: c}
	l.adminToken = testkit.Bootstrap(t, pool)
	l.admin = testkit.Client(t, l.srv, l.adminToken)
	l.adminID = me(t, l.srv, l.adminToken).Member.Id
	l.secondToken = testkit.PersonalToken(t, pool, id.FromBytes[access.Member](l.adminID), "deploy", access.AllScopes()...)
	l.second = testkit.Client(t, l.srv, l.secondToken)
	ctx := t.Context()

	l.firstChange = c.Now()
	l.mobile = createProject(t, l.admin, "mobile")
	c.Advance(time.Minute)
	l.qa = *do(l.second.CreateEnvironmentWithResponse(ctx, "mobile", management.EnvironmentCreate{Key: "qa", Name: "QA"})).JSON201
	c.Advance(time.Minute)
	do(l.second.UpdateEnvironmentWithResponse(ctx, "mobile", "qa", management.EnvironmentPatch{Name: ptr("Quality")}))
	c.Advance(time.Minute)
	l.pipeline = testkit.Client(t, l.srv, testkit.ProjectToken(t, pool, projectIDOf(l.mobile), access.RoleMaintainer))
	do(l.pipeline.CreateUnitTypeWithResponse(ctx, "mobile", management.UnitTypeCreate{Key: "device", Name: "Device"}))
	c.Advance(time.Minute)
	do(l.pipeline.CreateAttributeWithResponse(ctx, "mobile", management.AttributeCreate{Key: "plan_tier", Name: "Plan tier", Type: "string"}))
	l.lastInMobile = c.Now()
	c.Advance(time.Minute)
	l.web = createProject(t, l.admin, "web")
	return l
}

// actions lists the actions of a page, newest first.
func actions(t *testing.T, name string, res *management.ListProjectAuditEntriesResponse) []string {
	t.Helper()
	if res.JSON200 == nil {
		t.Fatalf("%s: GET audit = %d %s", name, res.StatusCode(), res.Body)
	}
	var out []string
	for _, e := range res.JSON200.Items {
		out = append(out, e.Action)
	}
	return out
}

func TestAuditFiltersSelectExactlyTheMatchingEntries(t *testing.T) {
	l := writeAuditLog(t)
	actorKind := management.ActorKind("token")
	qa := l.qa.Id
	from, to := l.firstChange.Add(time.Minute), l.lastInMobile.Add(-time.Minute)
	for name, tc := range map[string]struct {
		params management.ListProjectAuditEntriesParams
		want   []string
	}{
		"nothing": {management.ListProjectAuditEntriesParams{},
			[]string{"attribute.created", "unit_type.created", "environment.updated", "environment.created", "project.created"}},
		"a member, through both their tokens": {management.ListProjectAuditEntriesParams{Actor: &l.adminID},
			[]string{"environment.updated", "environment.created", "project.created"}},
		"an actor kind":  {management.ListProjectAuditEntriesParams{ActorKind: &actorKind}, []string{"attribute.created", "unit_type.created", "environment.updated", "environment.created", "project.created"}},
		"an action":      {management.ListProjectAuditEntriesParams{Action: ptr("environment.updated")}, []string{"environment.updated"}},
		"an entity kind": {management.ListProjectAuditEntriesParams{EntityKind: ptr("environment")}, []string{"environment.updated", "environment.created"}},
		"an entity":      {management.ListProjectAuditEntriesParams{EntityId: ptr(l.qa.Id.String())}, []string{"environment.updated", "environment.created"}},
		"an environment": {management.ListProjectAuditEntriesParams{Environment: &qa}, []string{"environment.updated", "environment.created"}},
		"a period, both ends included": {management.ListProjectAuditEntriesParams{From: &from, To: &to},
			[]string{"unit_type.created", "environment.updated", "environment.created"}},
		"text in an entity key, whatever its case": {management.ListProjectAuditEntriesParams{Q: ptr("QA")}, []string{"environment.updated", "environment.created"}},
		"text in an actor name":                    {management.ListProjectAuditEntriesParams{Q: ptr("pipeline")}, []string{"attribute.created", "unit_type.created"}},
		"text in an action":                        {management.ListProjectAuditEntriesParams{Q: ptr("unit_type.")}, []string{"unit_type.created"}},
		"an underscore matches itself":             {management.ListProjectAuditEntriesParams{Q: ptr("plan_tier")}, []string{"attribute.created"}},
		"wildcards match themselves":               {management.ListProjectAuditEntriesParams{Q: ptr("plan%tier")}, nil},
		"filters together": {management.ListProjectAuditEntriesParams{Actor: &l.adminID, EntityKind: ptr("project")},
			[]string{"project.created"}},
	} {
		got := actions(t, name, do(l.admin.ListProjectAuditEntriesWithResponse(t.Context(), "mobile", &tc.params)))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: actions = %v, want %v", name, got, tc.want)
		}
	}
}

func TestAuditFilterRefusesAnActorThatIsNoMemberID(t *testing.T) {
	srv := testkit.StartServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	for _, path := range []string{"/api/v1/projects/" + p.Key + "/audit?actor=not-a-uuid", "/api/v1/audit?actor=not-a-uuid"} {
		res := request(t, http.MethodGet, srv.URL+path, testkit.InstanceAdminToken(t))
		assertProblem(t, path, res.status, res.body, http.StatusBadRequest, "invalid_request")
	}
}

func TestOneAuditEntryIsReadByIDWithinItsProject(t *testing.T) {
	l := writeAuditLog(t)
	ctx := t.Context()
	list := do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{}))
	listed := list.JSON200.Items[0]
	got := do(l.admin.GetProjectAuditEntryWithResponse(ctx, "mobile", listed.Id.String()))
	if got.JSON200 == nil || got.JSON200.Id != listed.Id || got.JSON200.Action != listed.Action || !got.JSON200.OccurredAt.Equal(listed.OccurredAt) {
		t.Errorf("entry = %d %s, want %+v", got.StatusCode(), got.Body, listed)
	}
	if e := got.JSON200; e != nil && (e.Actor.Kind != "token" || e.Actor.Name != "pipeline" || e.Actor.MemberId != nil ||
		e.Entity.Key != "plan_tier" || e.Before != nil || e.RequestId == nil || e.ProjectId == nil || *e.ProjectId != l.mobile.Id) {
		t.Errorf("entry = %+v", e)
	}

	webEntry := do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "web", &management.ListProjectAuditEntriesParams{})).JSON200.Items[0]
	instance := do(l.admin.ListAuditEntriesWithResponse(ctx, &management.ListAuditEntriesParams{ActorKind: ptr(management.ActorKind("cli"))}))
	for name, entry := range map[string]string{
		"another project's entry": webEntry.Id.String(),
		"an instance entry":       instance.JSON200.Items[0].Id.String(),
		"an unknown identifier":   id.New[struct{}](id.NewSource(l.clock)).String(),
		"no identifier":           "nonsense",
	} {
		res := do(l.admin.GetProjectAuditEntryWithResponse(ctx, "mobile", entry))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusNotFound, "not_found")
	}
}

func TestNoTokenValueAppearsInTheAuditLog(t *testing.T) {
	l := writeAuditLog(t)
	res := do(l.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Limit: ptr(200)}))
	if res.JSON200 == nil {
		t.Fatalf("GET /audit = %d %s", res.StatusCode(), res.Body)
	}
	body := string(res.Body)
	for _, tok := range []string{l.adminToken, l.secondToken} {
		if strings.Contains(body, tok) || strings.Contains(body, tok[len(tok)-20:]) {
			t.Error("a token's value appears in the audit log")
		}
	}
	if !strings.Contains(body, `"hint":"`+l.adminToken[:11]+`"`) {
		t.Errorf("the bootstrap token's hint is not recorded: %s", body)
	}
}

func TestAuditPaginationYieldsEveryEntryOnceWhileMoreAreWritten(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey()).Key
	for i := range 6 {
		do(admin.CreateUnitTypeWithResponse(t.Context(), p, management.UnitTypeCreate{Key: "before_" + string(rune('a'+i)), Name: "x"}))
	}
	before := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p, &management.ListProjectAuditEntriesParams{})).JSON200.Items

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				do(admin.CreateUnitTypeWithResponse(t.Context(), p, management.UnitTypeCreate{Key: "during_" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)), Name: "x"}))
			}
		}
	})
	var paged []management.AuditEntry
	params := management.ListProjectAuditEntriesParams{Limit: ptr(3)}
	for {
		page := do(admin.ListProjectAuditEntriesWithResponse(t.Context(), p, &params)).JSON200
		if page == nil {
			t.Fatal("a page failed")
		}
		paged = append(paged, page.Items...)
		if page.NextCursor == nil {
			break
		}
		params.Cursor = page.NextCursor
	}
	close(stop)
	wg.Wait()

	seen := map[openapi_types.UUID]bool{}
	for i, e := range paged {
		if seen[e.Id] {
			t.Errorf("entry %s listed twice", e.Action)
		}
		seen[e.Id] = true
		if i > 0 && strings.Compare(e.Id.String(), paged[i-1].Id.String()) >= 0 {
			t.Errorf("entry %d is not older than the one before it", i)
		}
	}
	for _, e := range before {
		if !seen[e.Id] {
			t.Errorf("entry %s %s written before paging is missing", e.Action, e.Entity.Key)
		}
	}
}

func TestProjectAuditLogNeedsProjectRead(t *testing.T) {
	l := writeAuditLog(t)
	ctx := t.Context()
	pool := l.pool
	viewer := testkit.Client(t, l.srv, testkit.ProjectToken(t, pool, projectIDOf(l.mobile), access.RoleViewer, access.ScopeRead))
	list := do(viewer.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{}))
	if !status(t, "a viewer lists", list.StatusCode(), list.Body, http.StatusOK) {
		return
	}
	entry := list.JSON200.Items[0].Id.String()
	got := do(viewer.GetProjectAuditEntryWithResponse(ctx, "mobile", entry))
	status(t, "a viewer reads an entry", got.StatusCode(), got.Body, http.StatusOK)

	stranger := testkit.Client(t, l.srv, testkit.MemberToken(t, pool))
	refused := do(stranger.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{}))
	pr := assertProblem(t, "a member without a grant lists", refused.StatusCode(), refused.Body, http.StatusForbidden, "forbidden")
	if pr.Permission == nil || *pr.Permission != "project.read" {
		t.Errorf("permission = %v, want project.read", pr.Permission)
	}
	one := do(stranger.GetProjectAuditEntryWithResponse(ctx, "mobile", entry))
	assertProblem(t, "a member without a grant reads an entry", one.StatusCode(), one.Body, http.StatusForbidden, "forbidden")
}

func TestInstanceAuditLogSpansEveryProjectAndTheEntriesWithNone(t *testing.T) {
	l := writeAuditLog(t)
	ctx := t.Context()
	all := do(l.admin.ListAuditEntriesWithResponse(ctx, &management.ListAuditEntriesParams{}))
	if all.JSON200 == nil {
		t.Fatalf("GET /audit = %d %s", all.StatusCode(), all.Body)
	}
	projects := map[string]int{}
	for _, e := range all.JSON200.Items {
		key := "none"
		if e.ProjectId != nil {
			key = e.ProjectId.String()
		}
		projects[key]++
	}
	want := map[string]int{"none": 2, l.mobile.Id.String(): 5, l.web.Id.String(): 1}
	if len(projects) != len(want) || projects["none"] != 2 || projects[l.mobile.Id.String()] != 5 || projects[l.web.Id.String()] != 1 {
		t.Errorf("entries per project = %v, want %v", projects, want)
	}
	cli := do(l.admin.ListAuditEntriesWithResponse(ctx, &management.ListAuditEntriesParams{ActorKind: ptr(management.ActorKind("cli"))}))
	var cliActions []string
	for _, e := range cli.JSON200.Items {
		cliActions = append(cliActions, e.Action)
		if e.ProjectId != nil || e.RequestId != nil {
			t.Errorf("%s = %+v, want an instance entry outside any request", e.Action, e)
		}
	}
	if !slices.Equal(cliActions, []string{"api_token.created", "member.created"}) {
		t.Errorf("command line entries = %v", cliActions)
	}

	web := do(l.admin.ListAuditEntriesWithResponse(ctx, &management.ListAuditEntriesParams{Project: &l.web.Id}))
	if web.JSON200 == nil || len(web.JSON200.Items) != 1 || web.JSON200.Items[0].Action != "project.created" {
		t.Errorf("web's entries = %s", web.Body)
	}
	// A project that no longer exists, or never did, simply has no entries
	// left to list.
	unknown := openapi_types.UUID(id.New[struct{}](id.NewSource(l.clock)).Bytes())
	none := do(l.admin.ListAuditEntriesWithResponse(ctx, &management.ListAuditEntriesParams{Project: &unknown}))
	if none.JSON200 == nil || len(none.JSON200.Items) != 0 {
		t.Errorf("an unknown project's entries = %d %s, want none", none.StatusCode(), none.Body)
	}
}

func TestInstanceAuditLogIsForInstanceAdminsAlone(t *testing.T) {
	l := writeAuditLog(t)
	pool := l.pool
	for name, c := range map[string]*management.ClientWithResponses{
		"a member":        testkit.Client(t, l.srv, testkit.MemberToken(t, pool, access.AllScopes()...)),
		"a project token": l.pipeline,
	} {
		res := do(c.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{}))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusForbidden, "forbidden")
	}
	readOnly := testkit.Client(t, l.srv, testkit.PersonalToken(t, pool, id.FromBytes[access.Member](l.adminID), "read"))
	res := do(readOnly.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{}))
	status(t, "an instance admin's read token", res.StatusCode(), res.Body, http.StatusOK)
}

func TestAuditLogHasNoWriteOperation(t *testing.T) {
	l := writeAuditLog(t)
	count := func() int {
		return len(do(l.admin.ListAuditEntriesWithResponse(t.Context(), &management.ListAuditEntriesParams{Limit: ptr(200)})).JSON200.Items)
	}
	before := count()
	entry := do(l.admin.ListProjectAuditEntriesWithResponse(t.Context(), "mobile", &management.ListProjectAuditEntriesParams{})).JSON200.Items[0].Id
	for _, path := range []string{"/api/v1/audit", "/api/v1/projects/mobile/audit", "/api/v1/projects/mobile/audit/" + entry.String()} {
		for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
			res := request(t, method, l.srv.URL+path, l.adminToken)
			if res.status != http.StatusMethodNotAllowed && res.status != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404 or 405", method, path, res.status)
			}
		}
	}
	if after := count(); after != before {
		t.Errorf("entries = %d, want %d unchanged", after, before)
	}
}

func TestAuditEntriesArePaginatedNewestFirst(t *testing.T) {
	l := writeAuditLog(t)
	ctx := t.Context()
	all := do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{})).JSON200
	if all.NextCursor != nil {
		t.Errorf("one page holds every entry, yet next_cursor = %q", *all.NextCursor)
	}
	var paged []management.AuditEntry
	params := management.ListProjectAuditEntriesParams{Limit: ptr(2)}
	for pages := 0; ; pages++ {
		page := do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "mobile", &params)).JSON200
		paged = append(paged, page.Items...)
		if page.NextCursor == nil || pages > 10 {
			break
		}
		params.Cursor = page.NextCursor
	}
	if !slices.EqualFunc(paged, all.Items, func(a, b management.AuditEntry) bool { return a.Id == b.Id }) {
		t.Errorf("paged %d entries, want the %d of one page in the same order", len(paged), len(all.Items))
	}
	for _, limit := range []int{0, 201} {
		res := do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{Limit: ptr(limit)}))
		assertProblem(t, "limit", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
	res := do(l.admin.ListProjectAuditEntriesWithResponse(ctx, "mobile", &management.ListProjectAuditEntriesParams{Cursor: ptr("nonsense")}))
	assertProblem(t, "a malformed cursor", res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
}

func TestAuditEntriesStayWithinTheirProject(t *testing.T) {
	l := writeAuditLog(t)
	for key, want := range map[string]int{"mobile": 5, "web": 1} {
		page := do(l.admin.ListProjectAuditEntriesWithResponse(t.Context(), key, &management.ListProjectAuditEntriesParams{})).JSON200
		project := map[string]management.Project{"mobile": l.mobile, "web": l.web}[key]
		if len(page.Items) != want {
			t.Errorf("%s holds %d entries, want %d", key, len(page.Items), want)
		}
		for _, e := range page.Items {
			if e.ProjectId == nil || *e.ProjectId != project.Id {
				t.Errorf("%s lists %s of project %v", key, e.Action, e.ProjectId)
			}
		}
	}
}

type rawResponse struct {
	status int
	body   []byte
}

// request sends a request with no body as the holder of token.
func request(t *testing.T, method, url, token string) rawResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	if closeErr := res.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return rawResponse{status: res.StatusCode, body: body}
}
