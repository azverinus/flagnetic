package access_test

import (
	"testing"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestTheActorOfACallerIsItsTokenAndTheMemberItActsFor(t *testing.T) {
	member := access.Member{ID: id.MustParse[access.Member]("0190d2a0-0000-7000-8000-0000000000bb"), DisplayName: "Ada"}
	pat := access.APIToken{ID: id.MustParse[access.APIToken]("0190d2a0-0000-7000-8000-0000000000aa"), Name: "deploy", Kind: access.TokenPersonal}
	prj := access.APIToken{ID: id.MustParse[access.APIToken]("0190d2a0-0000-7000-8000-0000000000cc"), Name: "ci", Kind: access.TokenProject}
	for name, tc := range map[string]struct {
		caller access.Caller
		want   governance.Actor
	}{
		"personal access token": {access.TokenCaller(pat, &member), governance.Actor{
			Kind: governance.ActorToken, ID: pat.ID.String(), Name: "deploy", MemberID: member.ID.String(),
		}},
		"project token": {access.TokenCaller(prj, nil), governance.Actor{
			Kind: governance.ActorToken, ID: prj.ID.String(), Name: "ci",
		}},
		"no credential": {access.Caller{}, governance.Actor{}},
		"session": {access.Caller{Method: access.MethodSession, Member: &member}, governance.Actor{
			Kind: governance.ActorMember, ID: member.ID.String(), Name: "Ada", MemberID: member.ID.String(),
		}},
	} {
		if got := appaccess.ActorOf(tc.caller); got != tc.want {
			t.Errorf("%s: actor = %+v, want %+v", name, got, tc.want)
		}
	}
}
