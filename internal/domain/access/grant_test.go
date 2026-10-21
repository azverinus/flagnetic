package access_test

import (
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// The cap bites only once roles other than the built-ins exist, so it is
// shown with roles made up for the test.
func TestAGrantorHandsOutNoRoleHoldingAPermissionTheyLack(t *testing.T) {
	lead := access.Role{Name: "lead", Permissions: []access.Permission{access.PermProjectRead, access.PermGrantManage}}
	deployer := access.Role{Name: "deployer", Permissions: []access.Permission{access.PermProjectRead, access.PermEnvironmentManage}}
	held := []access.Permission{access.PermProjectRead, access.PermGrantManage}
	if err := access.CheckGrantorCap(held, lead); err != nil {
		t.Errorf("granting a role within what is held: %v", err)
	}
	err := access.CheckGrantorCap(held, deployer)
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "role_not_allowed" || e.Extensions["permission"] != "environment.manage" {
		t.Errorf("granting a role beyond what is held = %v, want role_not_allowed naming environment.manage", err)
	}
	if err := access.CheckGrantorCap(nil, access.Role{Name: "empty"}); err != nil {
		t.Errorf("a role holding nothing: %v", err)
	}
}

func TestEveryBuiltInRoleIsWithinAnOwnersReach(t *testing.T) {
	owner, _ := access.RoleNamed(access.RoleOwner)
	for _, r := range access.Roles() {
		if err := access.CheckGrantorCap(owner.Permissions, r); err != nil {
			t.Errorf("an owner granting %s: %v", r.Name, err)
		}
	}
}

func TestAGrantIsOfABuiltInRoleToAMemberWhoIsNotDeactivated(t *testing.T) {
	ids := newIDs()
	project := id.New[workspace.Project](ids)
	creator := id.New[access.Member](ids)
	for name, tc := range map[string]struct {
		status access.MemberStatus
		role   access.RoleName
		kind   apperrors.Kind
		code   string
	}{
		"an active member":       {access.MemberActive, access.RoleDeveloper, 0, ""},
		"an invited member":      {access.MemberInvited, access.RoleViewer, 0, ""},
		"a deactivated member":   {access.MemberDeactivated, access.RoleViewer, apperrors.KindConflict, "member_deactivated"},
		"a role that is no role": {access.MemberActive, "admin", apperrors.KindInvalid, "invalid_grant"},
	} {
		m := access.Member{ID: id.New[access.Member](ids), Status: tc.status}
		g, err := access.NewGrant(id.New[access.Grant](ids), access.NewGrantInput{
			Project: project, Member: m, Role: tc.role, CreatedBy: creator, Now: now,
		})
		if tc.code == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			} else if g.Member != m.ID || g.Role != tc.role || g.Source != access.GrantManual || g.CreatedBy != creator ||
				g.Project != project || !g.CreatedAt.Equal(now) {
				t.Errorf("%s: grant = %+v", name, g)
			}
			continue
		}
		if e, ok := apperrors.From(err); !ok || e.Kind != tc.kind || e.Code != tc.code {
			t.Errorf("%s: err = %v, want %s %s", name, err, tc.kind, tc.code)
		}
	}
}
