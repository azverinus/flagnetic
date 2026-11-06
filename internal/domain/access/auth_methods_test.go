package access_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

func TestAMemberKeepsAWayInWhileOneUsableRemains(t *testing.T) {
	set := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	enabled := access.IdentityMethod{ID: "enabled", ProviderEnabled: true}
	disabled := access.IdentityMethod{ID: "disabled"}
	tests := []struct {
		name            string
		methods         access.AuthMethods
		withoutPassword bool
		withoutIdentity string
		want            bool
	}{
		{"a password alone, kept", access.AuthMethods{Status: access.MemberActive, PasswordSetAt: &set}, false, "", true},
		{"a password alone, removed", access.AuthMethods{Status: access.MemberActive, PasswordSetAt: &set}, true, "", false},
		{"an identity alone, unlinked", access.AuthMethods{Status: access.MemberActive, Identities: []access.IdentityMethod{enabled}}, false, "enabled", false},
		{"a password beside an identity, identity unlinked", access.AuthMethods{
			Status: access.MemberActive, PasswordSetAt: &set, Identities: []access.IdentityMethod{enabled},
		}, false, "enabled", true},
		{"an identity beside a password, password removed", access.AuthMethods{
			Status: access.MemberActive, PasswordSetAt: &set, Identities: []access.IdentityMethod{enabled},
		}, true, "", true},
		{"two identities, one unlinked", access.AuthMethods{
			Status: access.MemberActive, Identities: []access.IdentityMethod{enabled, {ID: "other", ProviderEnabled: true}},
		}, false, "enabled", true},
		{"an identity at a disabled provider is no way in", access.AuthMethods{
			Status: access.MemberActive, Identities: []access.IdentityMethod{disabled, enabled},
		}, false, "enabled", false},
		{"nothing at all", access.AuthMethods{Status: access.MemberActive}, false, "", false},
		{"a deactivated member needs none", access.AuthMethods{Status: access.MemberDeactivated}, true, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.methods.KeepsWayIn(tc.withoutPassword, tc.withoutIdentity); got != tc.want {
				t.Errorf("KeepsWayIn(%v, %q) = %v, want %v", tc.withoutPassword, tc.withoutIdentity, got, tc.want)
			}
		})
	}
}

func TestReactivatingAMemberWhoHoldsAWayInMakesThemActive(t *testing.T) {
	m := access.Member{Status: access.MemberDeactivated}
	if got := m.Reactivated(true).Status; got != access.MemberActive {
		t.Errorf("a member holding a way in came back %s", got)
	}
	if got := m.Reactivated(false).Status; got != access.MemberInvited {
		t.Errorf("a member holding none came back %s", got)
	}
}
