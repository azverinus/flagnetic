// Package precondition is how a write names the version of the entity it was
// based on, and how that version is judged against the stored one. Every
// versioned entity's write requires one, so two people editing the same
// entity never silently overwrite each other: the second write is refused
// and its author sees what changed meanwhile.
package precondition

import (
	"errors"
	"strconv"
	"strings"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Version is the version a write was based on, or none when the request
// named none.
type Version struct {
	v   int64
	set bool
}

// None is the precondition of a write that names no version.
func None() Version { return Version{} }

// On is the precondition of a write based on version v.
func On(v int64) Version { return Version{v: v, set: true} }

// ErrRequired is a write that names no version where one is required. It
// carries no error kind: answering it is a matter of the protocol (HTTP
// answers 428 precondition_required), so each inbound adapter maps it.
var ErrRequired = errors.New("precondition: the write names no version")

// Check judges a write based on expected against the entity's current
// version: ErrRequired without a version, version_mismatch naming the
// current one when it moved on, nil otherwise.
func Check(expected Version, current int64) error {
	if !expected.set {
		return ErrRequired
	}
	if expected.v != current {
		return apperrors.PreconditionFailed("version_mismatch",
			"the entity changed since the version this write was based on; read it again").
			WithExtension("current_version", current)
	}
	return nil
}

// errMalformed refuses a version that is not one this API answered.
var errMalformed = errors.New("precondition: a version is a positive integer, quoted as an entity tag or bare")

// Parse reads a version as an entity tag (`"7"`) or bare (`7`). A weak tag,
// `*` or a list never names one version, so each is refused.
func Parse(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 1 || strconv.FormatInt(v, 10) != s {
		return Version{}, errMalformed
	}
	return On(v), nil
}

// Tag returns version v as the strong entity tag an ETag header carries.
func Tag(v int64) string { return `"` + strconv.FormatInt(v, 10) + `"` }
