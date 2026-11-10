package management_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// failingReceiver answers every call with a 500, as a receiver that is down
// does.
func failingReceiver(t *testing.T) *testkit.Receiver {
	t.Helper()
	return testkit.StartReceiver(t, testkit.Reply{Status: http.StatusInternalServerError, Body: "down"})
}

// disableByFailure makes the platform disable the project subscription
// webhook: a day on, twenty-five attempts in a row fail.
func disableByFailure(t *testing.T, in smtpInstance, c *clock.Fake, dead *testkit.Receiver, project string, webhook openapi_types.UUID) {
	t.Helper()
	c.Advance(24 * time.Hour)
	pings(t, in.admin, project, webhook, 25)
	deliverAll(t, in.srv, dead, 25)
	if w := subscription(t, in.admin, project, webhook); w.Enabled {
		t.Fatalf("after twenty-five failures over a day the subscription is %+v, want it disabled", w)
	}
}

// disableInstanceByFailure is disableByFailure for an instance subscription.
func disableInstanceByFailure(t *testing.T, in smtpInstance, c *clock.Fake, dead *testkit.Receiver, webhook openapi_types.UUID) {
	t.Helper()
	c.Advance(24 * time.Hour)
	for range 25 {
		if res := do(in.admin.TestInstanceWebhookWithResponse(t.Context(), webhook)); res.JSON202 == nil {
			t.Fatalf("POST /webhooks/{webhook}/test = %d: %s", res.StatusCode(), res.Body)
		}
	}
	deliverAll(t, in.srv, dead, 25)
	if res := do(in.admin.GetInstanceWebhookWithResponse(t.Context(), webhook)); res.JSON200 == nil || res.JSON200.Enabled {
		t.Fatalf("after twenty-five failures over a day the instance subscription is %s, want it disabled", res.Body)
	}
}

// wantSystemEntry fails unless the instance's audit log holds one
// webhook.disabled entry of webhook, written by the platform.
func wantSystemEntry(t *testing.T, in smtpInstance, webhook management.WebhookWithSecret) {
	t.Helper()
	entries := in.auditOf(t, "webhook.disabled", webhook.Id.String())
	if len(entries) != 1 || entries[0].Actor.Kind != management.ActorKindSystem {
		t.Errorf("webhook.disabled entries = %+v, want one by the system", entries)
	}
}

func TestADisabledProjectSubscriptionIsMailedToItsManagersInTheirLanguage(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(time.Now().UTC())
	in := startMailingInstance(t, testkit.MailpitOpen,
		[]testkit.ServerOption{reachable, testkit.WithClock(c), testkit.WithDefaultLocale("en")})
	p := createProject(t, in.admin, projectKey())
	pid := projectIDOf(p)
	grant := func(role access.RoleName, opts ...testkit.MemberOption) testkit.Member {
		m := testkit.NewMember(t, in.pool, opts...)
		testkit.Grant(t, in.pool, pid, m.ID, role)
		return m
	}
	owner := grant(access.RoleOwner, testkit.WithDisplayName("Katherine Johnson"))
	maintainer := grant(access.RoleMaintainer, testkit.WithLocale("ru"), testkit.WithDisplayName("Dorothy Vaughan"))
	// None of these manages webhooks of the project, or can be told.
	viewer := grant(access.RoleViewer)
	developer := grant(access.RoleDeveloper)
	gone := grant(access.RoleOwner)
	if res := do(in.admin.DeactivateMemberWithResponse(t.Context(), gone.ID.String())); res.StatusCode() != http.StatusOK {
		t.Fatalf("POST /members/{member}/deactivate = %d: %s", res.StatusCode(), res.Body)
	}
	elsewhere := testkit.NewMember(t, in.pool)
	testkit.Grant(t, in.pool, projectIDOf(createProject(t, in.admin, projectKey())), elsewhere.ID, access.RoleOwner)
	// An instance admin holds webhook.manage in every project.
	otherAdmin, _ := testkit.NewInstanceAdmin(t, in.pool, testkit.WithDisplayName("Margaret Hamilton"))

	dead := failingReceiver(t)
	failing := createWebhook(t, in.admin, p.Key, management.WebhookCreate{Name: "Security channel", Url: dead.URL, Events: []string{"flag.created"}})
	disableByFailure(t, in.smtpInstance, c, dead, p.Key, failing.Id)

	link := smtpBaseURL + "/projects/" + p.Key + "/webhooks/" + failing.Id.String()
	for name, want := range map[string]testkit.Member{"Katherine Johnson": owner, "Grace Hopper": in.member, "Margaret Hamilton": otherAdmin} {
		got := in.mp.AwaitMails(t, want.Email, 1)
		if len(got) != 1 {
			t.Fatalf("%d mails to %s, want one", len(got), want.Email)
		}
		m := got[0]
		if m.Subject != `Webhook "Security channel" was disabled` || m.From != "flags@example.com" {
			t.Errorf("mail to %s = %q from %s, want the English notice from the configured sender", want.Email, m.Subject, m.From)
		}
		for _, part := range []string{m.Text, m.HTML} {
			for _, needle := range []string{name, "Security channel", p.Name, "failing persistently", link} {
				if !strings.Contains(part, needle) {
					t.Errorf("mail to %s lacks %q:\n%s", want.Email, needle, part)
				}
			}
		}
		if !strings.Contains(m.HTML, `href="`+link+`"`) {
			t.Errorf("the HTML mail to %s does not link to %s: %s", want.Email, link, m.HTML)
		}
		// The receiver's address and secret are for the console, not for an
		// inbox.
		if strings.Contains(m.Text+m.HTML, dead.URL) || strings.Contains(m.Text+m.HTML, failing.Secret) {
			t.Errorf("the mail to %s names the receiver or its secret", want.Email)
		}
	}
	ru := in.mp.AwaitMails(t, maintainer.Email, 1)[0]
	if ru.Subject != `Вебхук «Security channel» отключён` || !strings.Contains(ru.Text, "Dorothy Vaughan") || !strings.Contains(ru.Text, link) {
		t.Errorf("mail to the maintainer = %q:\n%s\nwant the Russian notice with the link", ru.Subject, ru.Text)
	}
	for _, not := range []testkit.Member{viewer, developer, gone, elsewhere} {
		in.mp.AssertNoMail(t, not.Email)
	}
	if n := len(in.mp.MailsTo(t, owner.Email)); n != 1 {
		t.Errorf("the owner was mailed %d times, want once", n)
	}
	await(t, "the mails to be counted", func() bool {
		return testkit.Metric(t, in.srv.Scrape(t), `flagnetic_mail_sent_total{kind="webhook_disabled",result="sent"}`) >= 4
	})
}

func TestADisabledInstanceSubscriptionIsMailedToTheInstanceAdminsAlone(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(time.Now().UTC())
	in := startMailingInstance(t, testkit.MailpitOpen, []testkit.ServerOption{reachable, testkit.WithClock(c)})
	p := createProject(t, in.admin, projectKey())
	owner := testkit.NewMember(t, in.pool)
	testkit.Grant(t, in.pool, projectIDOf(p), owner.ID, access.RoleOwner)
	otherAdmin, _ := testkit.NewInstanceAdmin(t, in.pool)
	demoted := testkit.NewMember(t, in.pool, testkit.AsInstanceAdmin(), testkit.WithStatus(access.MemberDeactivated))

	dead := failingReceiver(t)
	archive := createInstanceWebhook(t, in.admin, management.WebhookCreate{Name: "Archive", Url: dead.URL, Events: []string{"audit.entry"}})
	disableInstanceByFailure(t, in.smtpInstance, c, dead, archive.Id)

	link := smtpBaseURL + "/admin/webhooks/" + archive.Id.String()
	for _, want := range []testkit.Member{in.member, otherAdmin} {
		m := in.mp.AwaitMails(t, want.Email, 1)[0]
		if m.Subject != `Webhook "Archive" was disabled` || !strings.Contains(m.Text, link) || !strings.Contains(m.HTML, `href="`+link+`"`) ||
			!strings.Contains(strings.ToLower(m.Text), "instance") {
			t.Errorf("mail to %s = %q:\n%s\nwant the notice of an instance subscription linking to %s", want.Email, m.Subject, m.Text, link)
		}
	}
	// A project's owner manages webhooks of that project, not the
	// instance's.
	in.mp.AssertNoMail(t, owner.Email)
	in.mp.AssertNoMail(t, demoted.Email)
}

func TestWithoutSMTPADisabledSubscriptionIsDisabledAndNothingIsMailed(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(time.Now().UTC())
	in := startSMTPInstance(t, []testkit.ServerOption{reachable, testkit.WithClock(c)})
	p := createProject(t, in.admin, projectKey())
	dead := failingReceiver(t)
	failing := createWebhook(t, in.admin, p.Key, management.WebhookCreate{Name: "Security channel", Url: dead.URL, Events: []string{"flag.created"}})

	disableByFailure(t, in, c, dead, p.Key, failing.Id)

	// A series is absent until a mail of its kind is counted.
	if scrape := in.srv.Scrape(t); strings.Contains(scrape, `kind="webhook_disabled"`) {
		t.Errorf("a webhook_disabled mail was counted with no SMTP server:\n%s", scrape)
	}
	wantSystemEntry(t, in, failing)
}

func TestADisabledSubscriptionStaysDisabledWhenItsMailFailsAndTheFailureIsCounted(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(time.Now().UTC())
	logs := testkit.NewLogSink()
	// The picky server refuses every recipient outside its one domain.
	in := startMailingInstance(t, testkit.MailpitPicky, []testkit.ServerOption{
		reachable, testkit.WithClock(c), testkit.WithEdge(func(o *flaghttp.Options) { o.Logger = logs.Logger() }),
	})
	p := createProject(t, in.admin, projectKey())
	dead := failingReceiver(t)
	failing := createWebhook(t, in.admin, p.Key, management.WebhookCreate{Name: "Security channel", Url: dead.URL, Events: []string{"flag.created"}})

	disableByFailure(t, in.smtpInstance, c, dead, p.Key, failing.Id)

	await(t, "the refused mail to be counted", func() bool {
		return testkit.Metric(t, in.srv.Scrape(t), `flagnetic_mail_sent_total{kind="webhook_disabled",result="rejected"}`) >= 1
	})
	await(t, "the failed mail to be logged", func() bool { return len(logs.Find("mail not sent")) > 0 })
	if rec := logs.Find("mail not sent")[0]; rec.Attrs["kind"] != "webhook_disabled" || rec.Attrs["failure"] != "rejected" {
		t.Errorf("log record = %+v, want the kind and the failure class", rec.Attrs)
	}
	if w := subscription(t, in.admin, p.Key, failing.Id); w.Enabled || w.DisabledReason == nil {
		t.Errorf("after a failed mail the subscription is %+v, want it still disabled", w)
	}
	wantSystemEntry(t, in.smtpInstance, failing)
}
