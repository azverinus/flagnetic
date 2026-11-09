package management_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

const invitationLinkPrefix = smtpBaseURL + "/invitations/"

// mailingInstance is an instance whose SMTP server is a Mailpit, its instance
// admin called Grace Hopper.
type mailingInstance struct {
	smtpInstance
	mp *testkit.Mailpit
}

func startMailingInstance(t *testing.T, mode testkit.MailpitMode, serverOpts []testkit.ServerOption,
	adminOpts ...testkit.MemberOption,
) mailingInstance {
	t.Helper()
	mp := testkit.StartMailpit(t, mode)
	in := startSMTPInstance(t, serverOpts, append([]testkit.MemberOption{testkit.WithDisplayName("Grace Hopper")}, adminOpts...)...)
	putSMTP(t, in.admin, settingsFor(mp))
	return mailingInstance{smtpInstance: in, mp: mp}
}

// inviteMailed invites email through c and fails unless the answer says the
// invitation is mailed and shows no link.
func inviteMailed(t *testing.T, c *management.ClientWithResponses, email string) management.MemberInvitation {
	t.Helper()
	res := do(c.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: email, DisplayName: "Ada Lovelace"}))
	if res.JSON201 == nil {
		t.Fatalf("POST /members = %d: %s", res.StatusCode(), res.Body)
	}
	wantMailed(t, "the invitation", res.JSON201)
	return *res.JSON201
}

// wantMailed fails unless mi says its invitation is mailed and shows no
// link.
func wantMailed(t *testing.T, what string, mi *management.MemberInvitation) {
	t.Helper()
	if mi.Invitation == nil || !mi.Invitation.Mailed || mi.Invitation.Url != nil {
		t.Fatalf("%s = %+v, want it mailed and its link not shown", what, mi.Invitation)
	}
}

// invitationLinkIn returns the invitation link the mail holds, in its text,
// and fails unless its HTML links to the same.
func invitationLinkIn(t *testing.T, m testkit.ReceivedMail) string {
	t.Helper()
	i := strings.Index(m.Text, invitationLinkPrefix)
	if i < 0 {
		t.Fatalf("the mail %q holds no link under %s: %s", m.Subject, invitationLinkPrefix, m.Text)
	}
	link := m.Text[i:]
	if end := strings.IndexAny(link, " \r\n"); end >= 0 {
		link = link[:end]
	}
	if len(link) != len(invitationLinkPrefix)+43 {
		t.Fatalf("link %q, want the console's origin, /invitations/ and 43 base64url characters", link)
	}
	if !strings.Contains(m.HTML, `href="`+link+`"`) {
		t.Errorf("the HTML does not link to %s: %s", link, m.HTML)
	}
	testkit.RecordSecret(secretOf(link))
	return link
}

func TestWithSMTPAnInvitationIsMailedAndItsLinkAccepts(t *testing.T) {
	t.Parallel()
	in := startMailingInstance(t, testkit.MailpitStartTLS, nil)
	email := newEmail()

	mi := inviteMailed(t, in.admin, email)
	got := in.mp.AwaitMails(t, email, 1)[0]
	if got.Subject != "Grace Hopper invited you to Flagnetic" || !strings.Contains(got.Text, "Ada Lovelace") ||
		!strings.Contains(got.Text, email) || strings.Contains(got.Text, "project") {
		t.Errorf("mail = %q %q, want the English invitation to the instance by Grace Hopper, greeting Ada", got.Subject, got.Text)
	}
	if got.From != "flags@example.com" || len(got.To) != 1 || got.To[0] != email {
		t.Errorf("mail from %s to %v, want from the configured sender to %s", got.From, got.To, email)
	}
	link := invitationLinkIn(t, got)
	if expires := mi.Invitation.ExpiresAt.UTC().Format("2006-01-02 15:04"); !strings.Contains(got.Text, expires) {
		t.Errorf("the mail does not say the link expires at %s UTC:\n%s", expires, got.Text)
	}

	// The mailed link is the invitation: it opens, sets the first password
	// and signs in.
	if shown := opened(t, in.srv, link); shown.JSON200 == nil || shown.JSON200.DisplayName != "Ada Lovelace" ||
		shown.JSON200.InvitedBy == nil || *shown.JSON200.InvitedBy != "Grace Hopper" {
		t.Fatalf("GET the mailed invitation = %d: %s", shown.StatusCode(), shown.Body)
	}
	if res := accept(t, testkit.NewBrowser(t, in.srv.URL), link, newPassword); res.JSON200 == nil {
		t.Fatalf("accepting the mailed invitation = %d: %s", res.StatusCode(), res.Body)
	}
	if m := memberOf(t, in.admin, memberRef(mi.Member.Id)); m.Status != management.MemberStatusActive {
		t.Errorf("member after accepting = %+v, want active", m)
	}
}

func TestWithSMTPReissuingMailsAFreshLinkAndKillsTheOldOne(t *testing.T) {
	t.Parallel()
	in := startMailingInstance(t, testkit.MailpitOpen, nil)
	email := newEmail()
	mi := inviteMailed(t, in.admin, email)
	first := invitationLinkIn(t, in.mp.AwaitMails(t, email, 1)[0])

	res := do(in.admin.ReissueInvitationWithResponse(t.Context(), mi.Member.Id.String()))
	if res.JSON201 == nil {
		t.Fatalf("POST /members/%s/invitation = %d: %s", mi.Member.Id, res.StatusCode(), res.Body)
	}
	wantMailed(t, "the reissue", res.JSON201)
	second := invitationLinkIn(t, in.mp.AwaitMails(t, email, 2)[1])
	if second == first {
		t.Fatal("the reissue mailed the link it replaced")
	}
	wantGone(t, in.srv, first, "the link the reissue replaced")
	if acc := accept(t, testkit.NewBrowser(t, in.srv.URL), second, newPassword); acc.JSON200 == nil {
		t.Fatalf("accepting the reissued link = %d: %s", acc.StatusCode(), acc.Body)
	}
}

func TestWithSMTPAProjectInvitationIsMailedNamingTheProjectTheRoleAndTheInviter(t *testing.T) {
	t.Parallel()
	in := startMailingInstance(t, testkit.MailpitOpen, nil)
	p := createProject(t, in.admin, projectKey())
	owner := testkit.NewMember(t, in.pool, testkit.WithDisplayName("Katherine Johnson"))
	testkit.Grant(t, in.pool, projectIDOf(p), owner.ID, access.RoleOwner)
	ownerClient := testkit.Client(t, in.srv, testkit.PersonalToken(t, in.pool, owner.ID, "inviter",
		access.ScopeRead, access.ScopeFlagsWrite, access.ScopeExperimentsWrite, access.ScopeProjectAdmin))
	email := newEmail()

	res := do(ownerClient.InviteProjectMemberWithResponse(t.Context(), p.Key,
		management.ProjectInvitationCreate{Email: email, DisplayName: "Ada Lovelace", Role: "developer"}))
	if res.JSON201 == nil {
		t.Fatalf("POST /projects/%s/invitations = %d: %s", p.Key, res.StatusCode(), res.Body)
	}
	wantMailed(t, "the project invitation", res.JSON201)
	got := in.mp.AwaitMails(t, email, 1)[0]
	if want := "Katherine Johnson invited you to " + p.Name + " on Flagnetic"; got.Subject != want {
		t.Errorf("subject = %q, want %q", got.Subject, want)
	}
	for _, part := range []string{got.Text, got.HTML} {
		for _, want := range []string{"Katherine Johnson", p.Name, "Developer"} {
			if !strings.Contains(part, want) {
				t.Errorf("the mail does not name %q:\n%s", want, part)
			}
		}
	}

	// Re-inviting to the same project reissues the invitation, mailed again
	// with the new role.
	again := do(ownerClient.InviteProjectMemberWithResponse(t.Context(), p.Key,
		management.ProjectInvitationCreate{Email: email, DisplayName: "Ada Lovelace", Role: "viewer"}))
	if again.JSON201 == nil {
		t.Fatalf("inviting again = %d: %s", again.StatusCode(), again.Body)
	}
	wantMailed(t, "the second project invitation", again.JSON201)
	second := in.mp.AwaitMails(t, email, 2)[1]
	if !strings.Contains(second.Text, "Viewer") {
		t.Errorf("the second mail does not name the new role:\n%s", second.Text)
	}

	acceptedBy(t, in.srv, invitationLinkIn(t, second))
	if grants := grantsOf(t, in.admin, p.Key, email); len(grants) != 1 || grants[0].Role != "viewer" {
		t.Errorf("grants after accepting the mailed link = %+v, want the viewer role", grants)
	}
}

func TestAnInvitationIsMailedInTheInvitersLanguageUnlessTheInstanceKnowsTheInvitees(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		inviter, instance, subject string
	}{
		"the inviter's language":                   {inviter: "ru", instance: "en", subject: "Grace Hopper приглашает вас во Flagnetic"},
		"the inviter's language over the instance": {inviter: "en", instance: "ru", subject: "Grace Hopper invited you to Flagnetic"},
		"the instance's without the inviter's":     {inviter: "", instance: "ru", subject: "Grace Hopper приглашает вас во Flagnetic"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := startMailingInstance(t, testkit.MailpitOpen,
				[]testkit.ServerOption{testkit.WithDefaultLocale(tc.instance)}, testkit.WithLocale(tc.inviter))
			email := newEmail()
			inviteMailed(t, in.admin, email)
			got := in.mp.AwaitMails(t, email, 1)[0]
			if got.Subject != tc.subject {
				t.Errorf("subject = %q, want %q", got.Subject, tc.subject)
			}
			invitationLinkIn(t, got)
		})
	}
}

// A member the instance knows the language of, deactivated before they ever
// set a password, is mailed the invitation their reactivation issues in their
// own language, over the instance admin's and the instance's.
func TestAnInvitationIsMailedInTheInviteesLanguageWhenTheInstanceKnowsIt(t *testing.T) {
	t.Parallel()
	in := startMailingInstance(t, testkit.MailpitOpen,
		[]testkit.ServerOption{testkit.WithDefaultLocale("en")}, testkit.WithLocale("en"))
	email := newEmail()
	invitee := testkit.NewPasswordlessMember(t, in.pool, testkit.WithEmail(email), testkit.WithLocale("ru"),
		testkit.WithStatus(access.MemberDeactivated))

	res := do(in.admin.ReactivateMemberWithResponse(t.Context(), invitee.ID.String()))
	if res.JSON200 == nil {
		t.Fatalf("reactivating = %d: %s", res.StatusCode(), res.Body)
	}
	wantMailed(t, "the reactivation's invitation", res.JSON200)
	got := in.mp.AwaitMails(t, email, 1)[0]
	if want := "Приглашение во Flagnetic"; got.Subject != want {
		t.Errorf("subject = %q, want %q", got.Subject, want)
	}
	invitationLinkIn(t, got)
}

// An instance admin's invitation carrying a role in a project is mailed as
// one to the project, naming it, the role and the instance admin.
func TestWithSMTPAnInstanceInvitationWithAGrantIsMailedNamingTheProject(t *testing.T) {
	t.Parallel()
	in := startMailingInstance(t, testkit.MailpitOpen, nil)
	p := createProject(t, in.admin, projectKey())
	email := newEmail()

	res := do(in.admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{
		Email: email, DisplayName: "Ada Lovelace", Grant: &management.InvitationGrantCreate{Project: p.Key, Role: "maintainer"},
	}))
	if res.JSON201 == nil {
		t.Fatalf("POST /members = %d: %s", res.StatusCode(), res.Body)
	}
	wantMailed(t, "the invitation", res.JSON201)
	got := in.mp.AwaitMails(t, email, 1)[0]
	if want := "Grace Hopper invited you to " + p.Name + " on Flagnetic"; got.Subject != want {
		t.Errorf("subject = %q, want %q", got.Subject, want)
	}
	if !strings.Contains(got.Text, "Maintainer") || !strings.Contains(got.HTML, "Maintainer") {
		t.Errorf("the mail does not name the role:\n%s", got.Text)
	}
	acceptedBy(t, in.srv, invitationLinkIn(t, got))
	if grants := grantsOf(t, in.admin, p.Key, email); len(grants) != 1 || grants[0].Role != "maintainer" {
		t.Errorf("grants after accepting the mailed link = %+v, want the maintainer role", grants)
	}
}

// A member deactivated before they accepted is invited again when brought
// back, by nobody in particular, and that invitation is mailed too.
func TestAReactivatedMemberIsMailedTheirFreshInvitation(t *testing.T) {
	t.Parallel()
	in := startMailingInstance(t, testkit.MailpitOpen, nil)
	email := newEmail()
	mi := inviteMailed(t, in.admin, email)
	first := invitationLinkIn(t, in.mp.AwaitMails(t, email, 1)[0])
	if res := do(in.admin.DeactivateMemberWithResponse(t.Context(), mi.Member.Id.String())); res.StatusCode() != http.StatusOK {
		t.Fatalf("deactivating = %d: %s", res.StatusCode(), res.Body)
	}

	res := do(in.admin.ReactivateMemberWithResponse(t.Context(), mi.Member.Id.String()))
	if res.JSON200 == nil {
		t.Fatalf("reactivating = %d: %s", res.StatusCode(), res.Body)
	}
	wantMailed(t, "the reactivation's invitation", res.JSON200)
	got := in.mp.AwaitMails(t, email, 2)[1]
	if got.Subject != "You are invited to Flagnetic" {
		t.Errorf("subject = %q, want the invitation of nobody in particular", got.Subject)
	}
	wantGone(t, in.srv, first, "the link consumed at deactivation")
	if acc := accept(t, testkit.NewBrowser(t, in.srv.URL), invitationLinkIn(t, got), newPassword); acc.JSON200 == nil {
		t.Fatalf("accepting = %d: %s", acc.StatusCode(), acc.Body)
	}
}

func TestOnceSMTPIsRemovedAnInvitationShowsItsLinkAgain(t *testing.T) {
	t.Parallel()
	in := startMailingInstance(t, testkit.MailpitOpen, nil)
	if res := do(in.admin.DeleteMailSettingsWithResponse(t.Context())); res.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE /instance/smtp = %d: %s", res.StatusCode(), res.Body)
	}
	email := newEmail()
	res := do(in.admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: email, DisplayName: "Unmailed"}))
	if res.JSON201 == nil {
		t.Fatalf("POST /members = %d: %s", res.StatusCode(), res.Body)
	}
	recordLink(t, res.JSON201)
	if inv := res.JSON201.Invitation; inv.Mailed || !strings.HasPrefix(*inv.Url, invitationLinkPrefix) {
		t.Errorf("invitation = %+v, want its link shown and nothing mailed", inv)
	}
	// Mail leaves after the answer: a mail to a member of its own, sent last,
	// shows the instance has done what it would have for the first.
	other := newEmail()
	putSMTP(t, in.admin, settingsFor(in.mp))
	inviteMailed(t, in.admin, other)
	in.mp.AwaitMails(t, other, 1)
	in.mp.AssertNoMail(t, email)
}

func TestAnInvitationWhoseMailFailsStandsAndTheFailureIsCountedAndLogged(t *testing.T) {
	t.Parallel()
	logs := testkit.NewLogSink()
	// The picky server refuses every recipient outside its one domain.
	in := startMailingInstance(t, testkit.MailpitPicky,
		[]testkit.ServerOption{testkit.WithEdge(func(o *flaghttp.Options) { o.Logger = logs.Logger() })})
	email := newEmail()

	res := do(in.admin.InviteMemberWithResponse(t.Context(), management.MemberInvite{Email: email, DisplayName: "Bounced"}))
	if res.JSON201 == nil {
		t.Fatalf("POST /members = %d: %s, want 201 whatever the mail server says", res.StatusCode(), res.Body)
	}
	wantMailed(t, "the invitation", res.JSON201)
	series := `flagnetic_mail_sent_total{kind="invitation",result="rejected"}`
	await(t, "the refused mail to be counted", func() bool { return testkit.Metric(t, in.srv.Scrape(t), series) >= 1 })
	await(t, "the failed mail to be logged", func() bool { return len(logs.Find("mail not sent")) > 0 })
	rec := logs.Find("mail not sent")[0]
	if rec.Attrs["kind"] != "invitation" || rec.Attrs["failure"] != "rejected" ||
		rec.Attrs["request_id"] != res.HTTPResponse.Header.Get("X-Request-ID") {
		t.Errorf("log record = %+v, want the kind, the failure class and the request ID", rec.Attrs)
	}
	for _, r := range logs.Records() {
		for k, v := range r.Attrs {
			if strings.Contains(r.Message+k+fmt.Sprint(v), "/invitations/") {
				t.Errorf("a log record carries the invitation's link: %+v", r)
			}
		}
	}
	// The invitation stands, and reissuing it is how the person gets a mail.
	if m := memberOf(t, in.admin, memberRef(res.JSON201.Member.Id)); m.Status != management.MemberStatusInvited {
		t.Errorf("member = %+v, want invited", m)
	}
	if n := len(in.auditOf(t, "member.invited", res.JSON201.Member.Id.String())); n != 1 {
		t.Errorf("member.invited entries = %d, want 1", n)
	}
}
