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
		{"removing what is no way in takes nothing away", access.AuthMethods{
			Status: access.MemberActive, Identities: []access.IdentityMethod{disabled},
		}, false, "disabled", true},
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

func TestOnlyAUsableWayInCountsWhateverTheMembersStatus(t *testing.T) {
	set := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		methods access.AuthMethods
		want    bool
	}{
		{"a password", access.AuthMethods{Status: access.MemberDeactivated, PasswordSetAt: &set}, true},
		{"an identity at an enabled provider", access.AuthMethods{
			Status: access.MemberDeactivated, Identities: []access.IdentityMethod{{ID: "enabled", ProviderEnabled: true}},
		}, true},
		{"an identity at a disabled provider only", access.AuthMethods{
			Status: access.MemberDeactivated, Identities: []access.IdentityMethod{{ID: "disabled"}},
		}, false},
		{"nothing at all", access.AuthMethods{Status: access.MemberDeactivated}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.methods.HoldsUsableWayIn(); got != tc.want {
				t.Errorf("HoldsUsableWayIn() = %v, want %v", got, tc.want)
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

func TestEnforcementAndLocalLoginDecideWhichWaysInAreUsable(t *testing.T) {
	set := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	corp := access.IdentityMethod{ID: "corp", Provider: "corp", ProviderEnabled: true}
	other := access.IdentityMethod{ID: "other", Provider: "other", ProviderEnabled: true}
	enforced := access.DefaultAuthSettings
	enforced.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: "corp.example", Provider: "corp"}}
	closed := access.DefaultAuthSettings
	closed.LocalLoginEnabled = false
	tests := []struct {
		name     string
		settings access.AuthSettings
		email    string
		methods  access.AuthMethods
		want     bool
	}{
		{"a password, local login on", access.DefaultAuthSettings, "ann@corp.example",
			access.AuthMethods{PasswordSetAt: &set}, true},
		{"a password, local login off", closed, "ann@corp.example", access.AuthMethods{PasswordSetAt: &set}, false},
		{"a password in an enforced domain", enforced, "Ann@CORP.example", access.AuthMethods{PasswordSetAt: &set}, false},
		{"a password outside the enforced domain", enforced, "ann@sub.corp.example", access.AuthMethods{PasswordSetAt: &set}, true},
		{"the enforced provider's identity", enforced, "ann@corp.example",
			access.AuthMethods{Identities: []access.IdentityMethod{corp}}, true},
		{"another provider's identity in an enforced domain", enforced, "ann@corp.example",
			access.AuthMethods{Identities: []access.IdentityMethod{other}}, false},
		{"any provider's identity with local login off", closed, "ann@corp.example",
			access.AuthMethods{Identities: []access.IdentityMethod{other}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.methods
			m.Status = access.MemberActive
			if got := m.UnderSettings(tc.settings, tc.email).HoldsUsableWayIn(); got != tc.want {
				t.Errorf("HoldsUsableWayIn() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRemovingAWayInEnforcementRefusesTakesNothingAway(t *testing.T) {
	set := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	enforced := access.DefaultAuthSettings
	enforced.SSOEnforcedDomains = []access.EnforcedDomain{{Domain: "corp.example", Provider: "corp"}}
	m := access.AuthMethods{
		Status: access.MemberActive, PasswordSetAt: &set,
		Identities: []access.IdentityMethod{{ID: "corp", Provider: "corp", ProviderEnabled: true}, {ID: "other", Provider: "other", ProviderEnabled: true}},
	}.UnderSettings(enforced, "ann@corp.example")
	if !m.KeepsWayIn(true, "") {
		t.Error("removing a password the domain refuses was refused")
	}
	if !m.KeepsWayIn(false, "other") {
		t.Error("unlinking another provider's identity was refused")
	}
	if m.KeepsWayIn(false, "corp") {
		t.Error("unlinking the enforced provider's identity, the last usable way in, was allowed")
	}
}
