package access

import (
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
)

// ActorOf returns the actor audit entries name for a change caller makes: the
// token presented, with the member it acts for, or the member signed in. A
// caller without a credential has no actor: the zero one, which no audit
// entry accepts.
func ActorOf(caller access.Caller) governance.Actor {
	if t := caller.Token; t != nil {
		a := governance.Actor{Kind: governance.ActorToken, ID: t.ID.String(), Name: t.Name}
		if caller.Member != nil {
			a.MemberID = caller.Member.ID.String()
		}
		return a
	}
	m := caller.Member
	if m == nil {
		return governance.Actor{}
	}
	return governance.Actor{Kind: governance.ActorMember, ID: m.ID.String(), Name: m.DisplayName, MemberID: m.ID.String()}
}
