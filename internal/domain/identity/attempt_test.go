package identity_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestALinkAttemptReturnsToThePageItWasStartedFromOrToTheSettings(t *testing.T) {
	member, session := id.New[identity.Member](id.NewSource(clock.System{})), id.New[identity.Session](id.NewSource(clock.System{}))
	for name, tc := range map[string]struct{ returnTo, want string }{
		"a path of the site":  {"/settings/profile?tab=a", "/settings/profile?tab=a"},
		"the home page":       {"/", "/"},
		"none":                {"", "/settings"},
		"another site":        {"https://evil.example/x", "/settings"},
		"a protocol-relative": {"//evil.example", "/settings"},
		"a backslash":         {`/\evil.example`, "/settings"},
	} {
		t.Run(name, func(t *testing.T) {
			a := identity.NewLinkAttempt(id.New[identity.Attempt](id.NewSource(clock.System{})), id.New[identity.Provider](id.NewSource(clock.System{})),
				member, session, tc.returnTo, "", identity.Secrets{}, time.Now())
			if a.ReturnTo != tc.want || a.Intent != identity.IntentLink || a.Member != member || a.Session != session {
				t.Errorf("link attempt = %+v, want it to return to %q for %v in %v", a, tc.want, member, session)
			}
		})
	}
}
