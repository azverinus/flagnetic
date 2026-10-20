package eval

import (
	"cmp"
	"strings"
)

// Semver is a parsed semantic version. Build metadata is dropped because it
// never affects precedence.
type Semver struct {
	core       [3]string // numeric components without leading zeros; "0" when missing
	prerelease []string
}

// ParseSemver parses an optional lower-case "v", one to three dot-separated
// non-negative integers (missing components are 0, so "1.17" equals
// "1.17.0"), an optional "-" pre-release and an optional "+" build. Numeric
// components are unbounded and leading zeros are ignored. Pre-release and
// build identifiers are non-empty runs of [0-9A-Za-z-]. Anything else is
// not a version and makes a semver condition false.
func ParseSemver(s string) (Semver, bool) {
	var v Semver
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !validIdentifiers(s[i+1:]) {
			return Semver{}, false
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if !validIdentifiers(s[i+1:]) {
			return Semver{}, false
		}
		v.prerelease = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return Semver{}, false
	}
	v.core = [3]string{"0", "0", "0"}
	for i, p := range parts {
		if !isNumeric(p) {
			return Semver{}, false
		}
		v.core[i] = trimZeros(p)
	}
	return v, true
}

// Compare returns -1, 0 or 1 by SemVer 2.0.0 §11 precedence.
func (v Semver) Compare(w Semver) int {
	for i := range v.core {
		if c := cmpNumeric(v.core[i], w.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.prerelease) == 0 && len(w.prerelease) == 0:
		return 0
	case len(v.prerelease) == 0:
		return 1
	case len(w.prerelease) == 0:
		return -1
	}
	for i := 0; i < len(v.prerelease) && i < len(w.prerelease); i++ {
		if c := cmpIdentifier(v.prerelease[i], w.prerelease[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(v.prerelease), len(w.prerelease))
}

// cmpIdentifier orders pre-release identifiers: numeric ones numerically
// and below alphanumeric ones, alphanumeric ones in ASCII order.
func cmpIdentifier(a, b string) int {
	an, bn := isNumeric(a), isNumeric(b)
	switch {
	case an && bn:
		return cmpNumeric(trimZeros(a), trimZeros(b))
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

// cmpNumeric compares digit strings without leading zeros of any length.
func cmpNumeric(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func trimZeros(s string) string {
	if t := strings.TrimLeft(s, "0"); t != "" {
		return t
	}
	return "0"
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func validIdentifiers(s string) bool {
	for id := range strings.SplitSeq(s, ".") {
		if id == "" {
			return false
		}
		for i := 0; i < len(id); i++ {
			if !isIdentifierChar(id[i]) {
				return false
			}
		}
	}
	return true
}

func isIdentifierChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-'
}
