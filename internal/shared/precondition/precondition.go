// Package precondition is how a write names the version of the entity it was
// based on, and how that version is judged against the stored one. Every
// versioned entity's write requires one, so two people editing the same
// entity never silently overwrite each other: the second write is refused
// and its author sees what changed meanwhile.
package precondition

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Tags is the If-Match header of a request, read as RFC 9110 reads it: `*`, or
// a list of entity tags. Only strong tags are kept, since a weak one never
// matches under the strong comparison If-Match uses.
type Tags struct {
	// Any is `*`: it matches whatever the entity's current tag is, as long as
	// the entity exists.
	Any bool
	// Strong are the strong tags the header lists, without their quotes.
	Strong []string
}

// Matches reports whether the header holds the entity's current tag.
func (t Tags) Matches(current string) bool {
	return t.Any || slices.Contains(t.Strong, current)
}

// errMalformed refuses a header that is no `*` and no list of entity tags.
var errMalformed = errors.New(`precondition: If-Match is "*" or a list of entity tags, quoted as an ETag answered them or bare`)

// ParseTags reads an If-Match header: `*`, or entity tags separated by commas,
// each strong (`"7"`, or bare, `7`) or weak (`W/"7"`, which matches nothing).
// A quoted tag may hold a comma, and empty list elements (`"6", , "7"`) are
// skipped, as RFC 9110 asks of a list's recipient. A header that is none of
// these, or lists no tag at all, is refused.
func ParseTags(s string) (Tags, error) {
	if strings.TrimSpace(s) == "*" {
		return Tags{Any: true}, nil
	}
	var t Tags
	found := false
	rest := s
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		if rest[0] == ',' {
			rest = rest[1:]
			continue
		}
		weak := strings.HasPrefix(rest, "W/")
		rest = strings.TrimPrefix(rest, "W/")
		var tag string
		if rest != "" && rest[0] == '"' {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return Tags{}, errMalformed
			}
			tag, rest = rest[1:1+end], rest[2+end:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			tag, rest = strings.TrimRight(rest[:end], " \t"), rest[end:]
			if weak || tag == "" || tag == "*" || strings.ContainsAny(tag, "\" \t") {
				return Tags{}, errMalformed
			}
		}
		if rest = strings.TrimLeft(rest, " \t"); rest != "" && rest[0] != ',' {
			return Tags{}, errMalformed
		}
		found = true
		if !weak {
			t.Strong = append(t.Strong, tag)
		}
	}
	if !found {
		return Tags{}, errMalformed
	}
	return t, nil
}

// Version is the version a write was based on, or none when the request
// named none.
type Version struct {
	tags Tags
	set  bool
}

// None is the precondition of a write that names no version.
func None() Version { return Version{} }

// On is the precondition of a write based on version v.
func On(v int64) Version {
	return Version{set: true, tags: Tags{Strong: []string{strconv.FormatInt(v, 10)}}}
}

// ErrRequired is a write that names no version where one is required. It
// carries no error kind: answering it is a matter of the protocol (HTTP
// answers 428 precondition_required), so each inbound adapter maps it.
var ErrRequired = errors.New("precondition: the write names no version")

// Check judges a write based on expected against the entity's current
// version: ErrRequired without a version, version_mismatch naming the
// current one when it is not among those named (a weak tag names none), nil
// otherwise, `*` included.
func Check(expected Version, current int64) error {
	if !expected.set {
		return ErrRequired
	}
	if !expected.tags.Matches(strconv.FormatInt(current, 10)) {
		return apperrors.PreconditionFailed("version_mismatch",
			"the entity changed since the version this write was based on; read it again").
			WithExtension("current_version", current)
	}
	return nil
}

// Parse reads the versions a write names in If-Match, as ParseTags does: a
// version is an entity tag, quoted (`"7"`) or bare (`7`), and `*` and a list
// are read as RFC 9110 reads them.
func Parse(s string) (Version, error) {
	tags, err := ParseTags(s)
	if err != nil {
		return Version{}, err
	}
	return Version{set: true, tags: tags}, nil
}

// Tag returns version v as the strong entity tag an ETag header carries.
func Tag(v int64) string { return `"` + strconv.FormatInt(v, 10) + `"` }
