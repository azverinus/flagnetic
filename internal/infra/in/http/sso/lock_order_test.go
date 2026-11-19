package sso_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// settle gives a request time to run as far as it can before it waits on a
// lock held by the test.
func settle() { time.Sleep(500 * time.Millisecond) }

// A change made by an instance admin or through SCIM holds the instance admins
// and then waits for the member. A sign-in that completes an invitation must
// wait for the admins before it holds the member, or each waits for what the
// other holds and PostgreSQL aborts one of them.
func TestASignInActivatingAnInvitedMemberWaitsForTheAdminChangeToThatMember(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.autoProvision = false })
	project := e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", project, "developer"))
	address := uniqueEmail()
	_, invited := e.inviteTo(t, address, "", "")
	memberID, err := id.Parse[access.Member](invited.Id.String())
	if err != nil {
		t.Fatal(err)
	}

	idp.NextClaims(map[string]any{"email": address, "groups": []string{"eng"}})
	b := testkit.NewBrowser(t, e.srv.URL)
	o := e.opts()
	o.Stop = func(u *url.URL) bool { return strings.HasPrefix(u.Path, "/sso/complete/") }
	held := e.signIn(t, b, slug, "", o)
	if !strings.HasPrefix(held.URL.Path, "/sso/complete/") {
		t.Fatalf("the sign-in reached %s before completing", held.URL)
	}

	var (
		wg   sync.WaitGroup
		done testkit.Visit
	)
	store := pgaccess.NewStore(e.pool)
	err = postgres.NewTransactor(e.pool).Within(t.Context(), func(ctx context.Context) error {
		if err := store.LockInstanceAdmins(ctx); err != nil {
			return err
		}
		wg.Go(func() { done = b.Visit(t, held.URL.String(), e.opts()) })
		// The completion runs as far as it can: before the fix as far as the
		// member, whom it then holds while it waits for the admins.
		settle()
		_, _, err := store.LockMember(ctx, memberID)
		return err
	})
	wg.Wait()
	if err != nil {
		t.Fatalf("the change of the member: %v", err)
	}
	if done.URL.Path != "/" {
		t.Fatalf("the completion ended at %s, want it to sign in", done.URL)
	}
	if got := e.statusOf(t, invited.Id); got != management.MemberStatusActive {
		t.Errorf("the invited member is %s, want active", got)
	}
	e.wantGrants(t, project, invited.Id, management.GrantSourceSso, slug, "developer")
}

// Unlinking a member's last identity at a provider deletes the groups they
// claimed and takes back what the mappings gave. A replacement of the mappings
// made at the same time must not read the claim before the unlink has taken it
// back and then grant from it again: those grants would have no claim and no
// later edit of the mappings would remove them.
func TestUnlinkingAnIdentityAndReplacingTheMappingsAtOnceLeaveNoGrantFromTheProvider(t *testing.T) {
	idp := testkit.StartForgingIDP(t)
	e := start(t)
	slug := e.forged(t, idp, func(p *provider) { p.autoProvision, p.linkByEmail = false, true })
	alpha, beta := e.project(t), e.project(t)
	e.mapGroups(t, slug, projectMapping("eng", alpha, "developer"))
	m := e.passwordMember(t)
	member := openapi_types.UUID(uuid.MustParse(m.ID.String()))

	idp.NextClaims(map[string]any{"sub": "sub-race", "email": m.Email, "email_verified": true, "groups": []string{"eng"}})
	if v := e.signIn(t, testkit.NewBrowser(t, e.srv.URL), slug, "", e.opts()); v.URL.Path != "/" {
		t.Fatalf("the sign-in ended at %s", v.URL)
	}
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug, "developer")
	linked := identityAt(t, methodsOf(t, m.b), slug)
	admin := e.session(t)
	version := mappingsVersion(t, admin, slug)

	var (
		wg       sync.WaitGroup
		unlinked *http.Response
		replaced int
	)
	store := pgaccess.NewStore(e.pool)
	err := postgres.NewTransactor(e.pool).Within(t.Context(), func(ctx context.Context) error {
		if err := store.LockInstanceAdmins(ctx); err != nil {
			return err
		}
		wg.Go(func() {
			res, err := m.b.UnlinkMyIdentityWithResponse(t.Context(), linked.Id.String())
			if err != nil {
				t.Errorf("unlinking: %v", err)
				return
			}
			unlinked = res.HTTPResponse
		})
		settle()
		wg.Go(func() {
			res, err := admin.ReplaceAuthProviderGroupMappingsWithResponse(t.Context(), slug,
				&management.ReplaceAuthProviderGroupMappingsParams{IfMatch: version},
				management.GroupMappings{Items: []management.GroupMapping{projectMapping("eng", alpha, "developer"), projectMapping("eng", beta, "viewer")}})
			if err != nil {
				t.Errorf("replacing the mappings: %v", err)
				return
			}
			replaced = res.StatusCode()
		})
		settle()
		return nil
	})
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if unlinked == nil || unlinked.StatusCode != http.StatusNoContent {
		t.Fatalf("the unlink = %+v, want 204", unlinked)
	}
	if replaced != http.StatusOK {
		t.Fatalf("the replacement = %d, want 200", replaced)
	}
	e.wantGrants(t, alpha, member, management.GrantSourceSso, slug)
	e.wantGrants(t, beta, member, management.GrantSourceSso, slug)
}
