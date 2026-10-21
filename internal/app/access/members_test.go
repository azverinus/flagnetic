package access_test

import (
	"slices"
	"sync"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// Through the API a demoted instance admin is refused before reaching the
// lock, so only the use cases themselves show two changes made at once.
func TestTwoInstanceAdminsDemotedAtOnceLeaveOne(t *testing.T) {
	pool := testkit.FreshPool(t)
	c := clock.System{}
	tx, store, audit := postgres.NewTransactor(pool), pgaccess.NewStore(pool), pggovernance.NewAuditLog(pool)
	update := appaccess.NewUpdateMember(tx, store, audit, c, id.NewSource(c))
	deactivate := appaccess.NewDeactivateMember(tx, store, audit, c, id.NewSource(c))
	req := governance.Request{Actor: cli}
	for round := range 10 {
		a := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
		b := testkit.NewMember(t, pool, testkit.AsInstanceAdmin())
		codes := make([]string, 2)
		var wg sync.WaitGroup
		wg.Go(func() {
			_, err := update.Handle(t.Context(), appaccess.UpdateMemberCommand{
				Member: a.ID, Change: access.MemberChange{InstanceAdmin: new(false)}, Request: req,
			})
			codes[0] = codeOf(err)
		})
		wg.Go(func() {
			_, err := deactivate.Handle(t.Context(), b.ID, req)
			codes[1] = codeOf(err)
		})
		wg.Wait()
		slices.Sort(codes)
		if !slices.Equal(codes, []string{"", "last_instance_admin"}) {
			t.Fatalf("round %d: refusals = %q, want one change and one last_instance_admin", round, codes)
		}
		// The admin left steps down before the next round, as nobody else could.
		for _, m := range []testkit.Member{a, b} {
			_ = store.SaveMember(t.Context(), access.Member{ID: m.ID, Email: m.Email, DisplayName: "Test Member", Status: access.MemberActive}, c.Now())
		}
	}
}
