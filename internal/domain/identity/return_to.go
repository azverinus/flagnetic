package identity

import (
	"net/url"
	"strings"
	"unicode"
)

// ValidReturnTo returns returnTo when it is a path on this site a sign-in may
// send the browser back to, and "/" for anything else, so a sign-in link can
// never end on another site. A path has a single leading slash, no backslash
// (browsers read one as a slash, so "/\evil.com" is a host), no control or
// whitespace character, and no scheme or host once parsed; the decoded path is
// held to the same, since an encoded backslash or slash can be decoded on the
// way.
func ValidReturnTo(returnTo string) string {
	const home = "/"
	if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") {
		return home
	}
	if strings.ContainsFunc(returnTo, func(r rune) bool {
		return r == '\\' || unicode.IsControl(r) || unicode.IsSpace(r)
	}) {
		return home
	}
	u, err := url.Parse(returnTo)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return home
	}
	if strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, `\`) {
		return home
	}
	return returnTo
}
