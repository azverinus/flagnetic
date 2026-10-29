package access_test

import (
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

func TestAMailIsWrittenInTheFirstLanguageThatIsKnown(t *testing.T) {
	for name, tc := range map[string]struct {
		fallback  string
		preferred []string
		want      string
	}{
		"the recipient's own": {"en", []string{"ru", "en"}, "ru"},
		"the inviter's when the recipient follows the browser": {"en", []string{"", "ru"}, "ru"},
		"the instance's when nobody chose":                     {"ru", []string{"", ""}, "ru"},
		"the instance's when none are given":                   {"en", nil, "en"},
		"a language the mail lacks is skipped":                 {"en", []string{"de", "ru"}, "ru"},
		"only unknown languages":                               {"en", []string{"de", "fr"}, "en"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := access.MailLocale(tc.fallback, tc.preferred...); got != tc.want {
				t.Errorf("MailLocale(%q, %q) = %q, want %q", tc.fallback, tc.preferred, got, tc.want)
			}
		})
	}
}
