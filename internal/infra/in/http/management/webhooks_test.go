package management_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func siem() management.WebhookCreate {
	return management.WebhookCreate{Name: "SIEM", Url: "https://receiver.example/hook", Events: []string{"flag.kill_switch"}}
}

func createWebhook(t *testing.T, c *management.ClientWithResponses, project string, body management.WebhookCreate) management.WebhookWithSecret {
	t.Helper()
	res := do(c.CreateWebhookWithResponse(t.Context(), project, body))
	if res.JSON201 == nil || res.HTTPResponse.Header.Get("ETag") != `"1"` {
		t.Fatalf("POST webhooks = %d, ETag %q: %s", res.StatusCode(), res.HTTPResponse.Header.Get("ETag"), res.Body)
	}
	return *res.JSON201
}

// webhookEntry returns the one audit entry of action on the subscription,
// its before and after decoded.
func webhookEntry(t *testing.T, srv *testkit.Server, action string, webhook management.WebhookWithSecret) (management.AuditEntry, map[string]any, map[string]any) {
	t.Helper()
	entries := auditOf(t, srv, action, webhook.Id.String())
	if len(entries) != 1 {
		t.Fatalf("%d entries of %s, want 1", len(entries), action)
	}
	e := entries[0]
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), webhook.Secret) {
		t.Errorf("the %s entry holds the secret", action)
	}
	before, _ := e.Before.(map[string]any)
	after, _ := e.After.(map[string]any)
	return e, before, after
}

func TestCreatingAWebhookSubscriptionShowsItsSecretOnceAndSealsIt(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	created := createWebhook(t, admin, p.Key, siem())

	if raw, err := base64.RawURLEncoding.DecodeString(created.Secret); err != nil || len(raw) != 32 {
		t.Errorf("secret %q: want base64url of 32 random bytes", created.Secret)
	}
	if !created.Enabled || created.DisabledReason != nil || created.LastSuccessAt != nil || created.ConsecutiveFailures != 0 ||
		created.PreviousSecretExpiresAt != nil || created.Version != 1 || len(created.Environments) != 0 ||
		created.CreatedBy == nil || created.CreatedBy.Kind != management.WebhookCreatorKindMember {
		t.Errorf("new subscription %+v", created)
	}

	// Stored sealed under the instance's secret key, never in the clear.
	stored, err := pggovernance.NewWebhooks(testkit.Pool(t)).Webhook(t.Context(), projectIDOf(p),
		id.FromBytes[governance.Webhook](created.Id))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := secret.NewKeyring(secret.New(testkit.SecretKey)).Open(secret.PurposeWebhookSecret, stored.SealedSecret)
	if strings.Contains(stored.SealedSecret, created.Secret) || err != nil || string(opened) != created.Secret {
		t.Errorf("stored %q, opening it = %q, %v", stored.SealedSecret, opened, err)
	}

	list := do(admin.ListWebhooksWithResponse(t.Context(), p.Key))
	if list.JSON200 == nil || len(list.JSON200.Items) != 1 || strings.Contains(string(list.Body), created.Secret) {
		t.Fatalf("list = %d: %s", list.StatusCode(), list.Body)
	}
	one := do(admin.GetWebhookWithResponse(t.Context(), p.Key, created.Id))
	if one.JSON200 == nil || one.JSON200.Id != created.Id || one.HTTPResponse.Header.Get("ETag") != `"1"` ||
		strings.Contains(string(one.Body), created.Secret) {
		t.Fatalf("get = %d: %s", one.StatusCode(), one.Body)
	}

	e, before, after := webhookEntry(t, srv, "webhook.created", created)
	if e.Entity.Kind != "webhook" || before != nil || after["secret"] != "[redacted]" || after["url"] != created.Url {
		t.Errorf("audit entry %+v, after %v", e, after)
	}
}

func TestAWebhookTargetTheInstanceWillNotCallIsRefused(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	for name, tc := range map[string]struct {
		url, code string
	}{
		"plain http":       {"http://receiver.example/hook", "insecure_url"},
		"loopback":         {"https://127.0.0.1:9/hook", "private_network_target"},
		"loopback in IPv6": {"https://[::1]:9/hook", "private_network_target"},
		"localhost":        {"https://localhost:9/hook", "private_network_target"},
		"a private net":    {"https://10.0.0.1/hook", "private_network_target"},
		"a link-local":     {"https://169.254.169.254/latest/meta-data", "private_network_target"},
		"not a URL":        {"not a url", "invalid_webhook"},
		"another scheme":   {"ftp://receiver.example/hook", "invalid_webhook"},
	} {
		body := siem()
		body.Url = tc.url
		res := do(admin.CreateWebhookWithResponse(t.Context(), p.Key, body))
		assertProblem(t, name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, tc.code)
	}
	unknown := siem()
	unknown.Events = []string{"flag.kill_switch", "flag.exploded"}
	res := do(admin.CreateWebhookWithResponse(t.Context(), p.Key, unknown))
	if pr := assertProblem(t, "an unknown event type", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "unknown_event_type"); pr.Errors == nil ||
		len(*pr.Errors) != 1 || (*pr.Errors)[0].Pointer != "/events/1" {
		t.Errorf("unknown event type violations %+v", pr.Errors)
	}
	if list := do(admin.ListWebhooksWithResponse(t.Context(), p.Key)); list.JSON200 == nil || len(list.JSON200.Items) != 0 {
		t.Errorf("refused creates left %s", list.Body)
	}
}

func TestALoopbackWebhookTargetIsAllowedWhenTheOperatorAllowsIt(t *testing.T) {
	srv := testkit.StartServer(t, testkit.WithWebhookTargets(governance.TargetPolicy{AllowHTTP: true, AllowPrivateNetworks: true}))
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())

	body := siem()
	body.Url = "http://127.0.0.1:9/hook"
	if created := createWebhook(t, admin, p.Key, body); created.Url != body.Url {
		t.Errorf("url %q", created.Url)
	}
}

func TestAWebhookSubscriptionIsEditedAndDeleted(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := createWebhook(t, admin, p.Key, siem())

	patched := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id, &management.UpdateWebhookParams{IfMatch: ifMatch(1)},
		management.WebhookPatch{Name: ptr("Security"), Events: &[]string{"*"}, Environments: &[]string{"production"}, Enabled: ptr(false)}))
	if patched.JSON200 == nil || patched.HTTPResponse.Header.Get("ETag") != `"2"` {
		t.Fatalf("patch = %d: %s", patched.StatusCode(), patched.Body)
	}
	got := *patched.JSON200
	if got.Name != "Security" || got.Enabled || !slices.Equal(got.Events, []string{"*"}) ||
		!slices.Equal(got.Environments, []string{"production"}) || got.Url != created.Url || got.Version != 2 {
		t.Errorf("patched %+v", got)
	}
	_, before, after := webhookEntry(t, srv, "webhook.updated", created)
	if before["name"] != "SIEM" || after["name"] != "Security" || before["secret"] != "[redacted]" || after["secret"] != "[redacted]" {
		t.Errorf("audit before %v after %v", before, after)
	}

	stale := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id, &management.UpdateWebhookParams{IfMatch: ifMatch(1)},
		management.WebhookPatch{Name: ptr("Other")}))
	assertProblem(t, "a stale version", stale.StatusCode(), stale.Body, http.StatusPreconditionFailed, "version_mismatch")
	missing := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id, &management.UpdateWebhookParams{},
		management.WebhookPatch{Name: ptr("Other")}))
	assertProblem(t, "no If-Match", missing.StatusCode(), missing.Body, http.StatusPreconditionRequired, "precondition_required")
	// An environment the project does not have would silently drop every
	// event, so it is refused, before the version is judged.
	unknown := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id, &management.UpdateWebhookParams{IfMatch: ifMatch(1)},
		management.WebhookPatch{Environments: &[]string{"nowhere"}}))
	if pr := assertProblem(t, "an unknown environment", unknown.StatusCode(), unknown.Body, http.StatusUnprocessableEntity, "invalid_webhook"); pr.Errors == nil ||
		(*pr.Errors)[0].Pointer != "/environments/0" {
		t.Errorf("violations %+v", pr.Errors)
	}
	same := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id, &management.UpdateWebhookParams{IfMatch: ifMatch(2)},
		management.WebhookPatch{Name: ptr("Security")}))
	if same.JSON200 == nil || same.JSON200.Version != 2 || len(auditOf(t, srv, "webhook.updated", created.Id.String())) != 1 {
		t.Errorf("an edit changing nothing = %d: %s", same.StatusCode(), same.Body)
	}

	if res := do(admin.DeleteWebhookWithResponse(t.Context(), p.Key, created.Id)); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", res.StatusCode(), res.Body)
	}
	gone := do(admin.GetWebhookWithResponse(t.Context(), p.Key, created.Id))
	assertProblem(t, "get after delete", gone.StatusCode(), gone.Body, http.StatusNotFound, "not_found")
	again := do(admin.DeleteWebhookWithResponse(t.Context(), p.Key, created.Id))
	assertProblem(t, "delete again", again.StatusCode(), again.Body, http.StatusNotFound, "not_found")
	_, before, after = webhookEntry(t, srv, "webhook.deleted", created)
	if after != nil || before["name"] != "Security" || before["secret"] != "[redacted]" {
		t.Errorf("delete entry before %v after %v", before, after)
	}
}

func TestAWebhookSubscriptionStaysEditableAfterAnEnvironmentItNamesIsDeleted(t *testing.T) {
	srv := testkit.StartServer(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	body := siem()
	body.Environments = &[]string{"staging", "production"}
	created := createWebhook(t, admin, p.Key, body)
	deleted := do(admin.DeleteEnvironmentWithResponse(t.Context(), p.Key, "staging", &management.DeleteEnvironmentParams{IfMatch: ifMatch(1)}))
	status(t, "delete staging", deleted.StatusCode(), deleted.Body, http.StatusNoContent)

	renamed := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id, &management.UpdateWebhookParams{IfMatch: ifMatch(1)},
		management.WebhookPatch{Name: ptr("Security"), Enabled: ptr(false)}))
	if renamed.JSON200 == nil || renamed.JSON200.Name != "Security" || renamed.JSON200.Enabled {
		t.Fatalf("an edit leaving the environments alone = %d: %s", renamed.StatusCode(), renamed.Body)
	}
	// Naming the environments again judges each of them anew.
	again := do(admin.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id, &management.UpdateWebhookParams{IfMatch: ifMatch(2)},
		management.WebhookPatch{Environments: &[]string{"production", "staging"}}))
	if pr := assertProblem(t, "the deleted environment named again", again.StatusCode(), again.Body, http.StatusUnprocessableEntity, "invalid_webhook"); pr.Errors == nil ||
		(*pr.Errors)[0].Pointer != "/environments/1" {
		t.Errorf("violations %+v", pr.Errors)
	}
}

func TestTheEventCatalogueIsReadableByAnyMember(t *testing.T) {
	srv := testkit.StartServer(t)
	res := do(testkit.Client(t, srv, testkit.MemberToken(t, testkit.Pool(t), access.ScopeRead)).ListWebhookEventTypesWithResponse(t.Context()))
	if res.JSON200 == nil {
		t.Fatalf("GET /webhook-event-types = %d: %s", res.StatusCode(), res.Body)
	}
	var types []string
	for _, e := range res.JSON200.Items {
		types = append(types, e.Type)
	}
	want := []string{}
	for _, e := range governance.EventCatalogue() {
		want = append(want, e.Type)
	}
	if !slices.Equal(types, want) || !slices.Contains(types, "audit.entry") || slices.Contains(types, "*") {
		t.Errorf("catalogue %v", types)
	}
}

func TestOnlyAMaintainerReadsAndManagesWebhookSubscriptions(t *testing.T) {
	srv := testkit.StartServer(t)
	pool := testkit.Pool(t)
	admin := testkit.Client(t, srv, testkit.InstanceAdminToken(t))
	p := createProject(t, admin, projectKey())
	created := createWebhook(t, admin, p.Key, siem())
	sent := ping(t, admin, p.Key, created.Id)

	operations := map[string]func(c *management.ClientWithResponses) (int, []byte){
		"list": func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.ListWebhooksWithResponse(t.Context(), p.Key))
			return res.StatusCode(), res.Body
		},
		"get": func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.GetWebhookWithResponse(t.Context(), p.Key, created.Id))
			return res.StatusCode(), res.Body
		},
		"create": func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.CreateWebhookWithResponse(t.Context(), p.Key, siem()))
			return res.StatusCode(), res.Body
		},
		"edit": func(c *management.ClientWithResponses) (int, []byte) {
			// Enabling and rotating move the version, in whichever order the
			// operations run.
			current := subscription(t, admin, p.Key, created.Id)
			res := do(c.UpdateWebhookWithResponse(t.Context(), p.Key, created.Id,
				&management.UpdateWebhookParams{IfMatch: ifMatch(current.Version)}, management.WebhookPatch{}))
			return res.StatusCode(), res.Body
		},
		"test": func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.TestWebhookWithResponse(t.Context(), p.Key, created.Id))
			return res.StatusCode(), res.Body
		},
		"enable": func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.EnableWebhookWithResponse(t.Context(), p.Key, created.Id))
			return res.StatusCode(), res.Body
		},
		"rotate secret": func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.RotateWebhookSecretWithResponse(t.Context(), p.Key, created.Id))
			return res.StatusCode(), res.Body
		},
		"redeliver": func(c *management.ClientWithResponses) (int, []byte) {
			res := do(c.RedeliverWebhookDeliveryWithResponse(t.Context(), p.Key, created.Id, sent.Id))
			return res.StatusCode(), res.Body
		},
		"delete": func(c *management.ClientWithResponses) (int, []byte) {
			// Another subscription's, so the maintainer's delete leaves the
			// one the other operations name.
			other := createWebhook(t, admin, p.Key, siem())
			res := do(c.DeleteWebhookWithResponse(t.Context(), p.Key, other.Id))
			return res.StatusCode(), res.Body
		},
	}
	for _, role := range []access.RoleName{access.RoleViewer, access.RoleDeveloper, access.RoleMaintainer, access.RoleOwner} {
		c := testkit.Client(t, srv, testkit.GrantedToken(t, pool, projectIDOf(p), role))
		allowed := role == access.RoleMaintainer || role == access.RoleOwner
		for name, call := range operations {
			status, body := call(c)
			if allowed {
				if status >= 300 {
					t.Errorf("%s by %s = %d: %s", name, role, status, body)
				}
				continue
			}
			if pr := assertProblem(t, name+" by "+string(role), status, body, http.StatusForbidden, "forbidden"); pr.Permission == nil ||
				*pr.Permission != "webhook.manage" {
				t.Errorf("%s by %s names %v", name, role, pr.Permission)
			}
		}
	}
}

func TestAProjectTokenCreatingASubscriptionIsNamedItsCreator(t *testing.T) {
	srv := testkit.StartServer(t)
	p := createProject(t, testkit.Client(t, srv, testkit.InstanceAdminToken(t)), projectKey())
	ci := pipeline(t, srv, p, "maintainer", management.ScopeProjectAdmin)

	created := createWebhook(t, ci, p.Key, siem())
	read := do(ci.GetWebhookWithResponse(t.Context(), p.Key, created.Id))
	if read.JSON200 == nil {
		t.Fatalf("get = %d: %s", read.StatusCode(), read.Body)
	}
	if c := read.JSON200.CreatedBy; c == nil || c.Kind != management.WebhookCreatorKindToken || c.Name != "ci" {
		t.Errorf("created by %+v, want the project token named ci", c)
	}
}
