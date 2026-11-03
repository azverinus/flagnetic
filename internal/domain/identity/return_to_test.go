package identity_test

import (
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

func TestReturnToKeepsOnlyAPathOnThisSite(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"a path", "/projects/x/flags?tab=a", "/projects/x/flags?tab=a"},
		{"the root", "/", "/"},
		{"a fragment", "/projects/x#top", "/projects/x#top"},
		{"empty", "", "/"},
		{"a relative path", "projects/x", "/"},
		{"a protocol-relative address", "//evil.com", "/"},
		{"a backslash after the slash", `/\evil.com`, "/"},
		{"an encoded backslash", "/%5Cevil.com", "/"},
		{"an encoded lowercase backslash", "/%5cevil.com", "/"},
		{"an encoded second slash", "/%2Fevil.com", "/"},
		{"a backslash further on", `/a\b`, "/"},
		{"an absolute address", "https://evil.com", "/"},
		{"a scheme", "javascript:alert(1)", "/"},
		{"a newline", "/a\nb", "/"},
		{"a carriage return", "/a\rb", "/"},
		{"a tab", "/a\tb", "/"},
		{"a space", "/a b", "/"},
		{"a null byte", "/a\x00b", "/"},
		{"a delete character", "/a\x7fb", "/"},
		{"a no-break space", "/a b", "/"},
		{"a malformed escape", "/%zz", "/"},
		{"an at sign host trick", "/@evil.com", "/@evil.com"},
		{"a leading slash and a scheme-like segment", "/https://evil.com", "/https://evil.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := identity.ValidReturnTo(tc.in); got != tc.want {
				t.Errorf("ValidReturnTo(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
