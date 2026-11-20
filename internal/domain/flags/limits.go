package flags

import apperrors "github.com/azverinus/flagnetic/internal/shared/errors"

// Bounds of what a project holds. Every replica and every SDK holds a
// project's whole ruleset in memory and receives it on every change, and a
// write is bounded by 1 MiB alone, so without these a project's ruleset
// grows without end one write at a time.
const (
	// maxFlags is how many flags a project holds, archived ones included.
	maxFlags = 5_000
	// maxSegments is how many segments a project holds.
	maxSegments = 1_000
	// maxVariations is how many variations a flag has at most.
	maxVariations = 50
	// maxEntryLength is how many characters a string value of a condition,
	// or an entry of its list, has at most.
	maxEntryLength = 1_000
)

// CheckFlagCount refuses creating a flag in a project that already holds
// existing, which is the most flags it may.
func CheckFlagCount(existing int) error {
	if existing < maxFlags {
		return nil
	}
	return apperrors.Invalid("invalid_flag", "the flag is not valid", apperrors.Violation{
		Pointer: "/key", Code: "too_many", Detail: "a project holds at most 5,000 flags, archived ones included",
		Params: map[string]any{"max": maxFlags},
	})
}

// CheckSegmentCount refuses creating a segment in a project that already
// holds existing, which is the most segments it may.
func CheckSegmentCount(existing int) error {
	if existing < maxSegments {
		return nil
	}
	return apperrors.Invalid("invalid_segment", "the segment is not valid", apperrors.Violation{
		Pointer: "/key", Code: "too_many", Detail: "a project holds at most 1,000 segments",
		Params: map[string]any{"max": maxSegments},
	})
}
