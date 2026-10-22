// Package workspace is the domain of where flags live: projects, their
// environments, and the unit types and attributes their targeting refers to.
// Other contexts refer to a project or an environment by the identifier
// types declared here, so every context means the same entity by the same
// type.
package workspace

import (
	"regexp"
	"strings"
	"unicode/utf8"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// keyPattern is what every key of the workspace matches: projects,
// environments, unit types and attributes. Keys appear in URLs, SDK calls
// and rules, so they are short, lower case and free of anything a URL or a
// rule would have to escape.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Bounds of the workspace's names and descriptions.
const (
	maxNameLength        = 200
	maxDescriptionLength = 2000
)

// fields checks the key, name and description an entity of the workspace is
// given, and answers every broken rule at once under code, such as
// invalid_project, so a form shows every field to fix in one round. It keeps
// the name and description trimmed.
type fields struct {
	code, detail string
	violations   []apperrors.Violation
	name         string
	description  string
}

func check(code, detail string) *fields { return &fields{code: code, detail: detail} }

func (f *fields) key(key string) *fields {
	if !keyPattern.MatchString(key) {
		f.violations = append(f.violations, apperrors.Violation{
			Pointer: "/key", Code: "invalid_key",
			Detail: "a key starts with a lower-case letter and has at most 64 lower-case letters, digits, hyphens and underscores",
			Params: map[string]any{"pattern": keyPattern.String()},
		})
	}
	return f
}

func (f *fields) nameOf(name string) *fields {
	f.name = strings.TrimSpace(name)
	if f.name == "" || utf8.RuneCountInString(f.name) > maxNameLength {
		f.violations = append(f.violations, apperrors.Violation{
			Pointer: "/name", Code: "invalid_name", Detail: "a name has 1 to 200 characters and is not blank",
			Params: map[string]any{"min": 1, "max": maxNameLength},
		})
	}
	return f
}

func (f *fields) descriptionOf(description string) *fields {
	f.description = strings.TrimSpace(description)
	if utf8.RuneCountInString(f.description) > maxDescriptionLength {
		f.violations = append(f.violations, apperrors.Violation{
			Pointer: "/description", Code: "too_long", Detail: "a description has at most 2,000 characters",
			Params: map[string]any{"max": maxDescriptionLength},
		})
	}
	return f
}

func (f *fields) err() error {
	if len(f.violations) == 0 {
		return nil
	}
	return apperrors.Invalid(f.code, f.detail, f.violations...)
}

// errKeyImmutable refuses a change to a key: everything that names the
// entity by its key (SDKs, rules, scripts, URLs) would silently stop
// matching it.
var errKeyImmutable = apperrors.Invalid("key_immutable", "a key never changes", apperrors.Violation{
	Pointer: "/key", Code: "key_immutable", Detail: "a key never changes; create a new entity under the new key instead",
})

// DiscoverableAttributeKey reports whether key could be declared as an
// attribute: a key of the workspace, not the reserved one. Only such keys
// are worth showing as discovered, since nothing else can be declared.
func DiscoverableAttributeKey(key string) bool {
	return keyPattern.MatchString(key) && key != reservedAttributeKey
}
