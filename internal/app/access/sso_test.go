package access_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func newSSO(t *testing.T) *appaccess.SSO {
	t.Helper()
	pool := testkit.Pool(t)
	c := testkit.Clock()
	store := pgaccess.NewStore(pool)
	return appaccess.NewSSO(store, store, testkit.Record(pool), c, id.NewSource(c), rand.Reader, bytes.Repeat([]byte{1}, 32))
}

func TestSSOFindsMembersByEmailAndIdentifierAndKnowsWhenThereIsNone(t *testing.T) {
	sso := newSSO(t)
	m := testkit.NewMember(t, testkit.Pool(t))

	byEmail, ok, err := sso.MemberByEmail(t.Context(), strings.ToUpper(m.Email))
	if err != nil || !ok || byEmail.ID != m.ID {
		t.Fatalf("MemberByEmail = %+v, %v, %v", byEmail, ok, err)
	}
	if _, ok, err = sso.MemberByEmail(t.Context(), "nobody@example.com"); err != nil || ok {
		t.Errorf("MemberByEmail of nobody = %v, %v", ok, err)
	}
	byID, ok, err := sso.MemberByID(t.Context(), m.ID)
	if err != nil || !ok || byID.Email != m.Email {
		t.Fatalf("MemberByID = %+v, %v, %v", byID, ok, err)
	}
	if _, ok, err = sso.MemberByID(t.Context(), id.New[access.Member](id.NewSource(testkit.Clock()))); err != nil || ok {
		t.Errorf("MemberByID of nobody = %v, %v", ok, err)
	}
}

func TestSSOProvisionsNoMemberForAnAddressThatIsNoneOrIsTaken(t *testing.T) {
	sso := newSSO(t)
	taken := testkit.NewMember(t, testkit.Pool(t))
	pool := testkit.Pool(t)
	tx := postgres.NewTransactor(pool)

	for name, email := range map[string]string{"not an address": "not-an-address", "a taken address": taken.Email} {
		err := tx.Within(t.Context(), func(ctx context.Context) error {
			_, err := sso.ProvisionMember(ctx, email, "Ada", "corp", "req")
			return err
		})
		if err == nil {
			t.Errorf("provisioning a member for %s succeeded", name)
		}
	}
}
