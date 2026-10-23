package experiments

import (
	"errors"
	"regexp"
)

// SaltLength is how many characters a salt has.
const SaltLength = 16

// saltPattern is the form of every salt of an experiment: lower-case hex.
var saltPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidSalt reports whether s has the form of an experiment's salt.
func ValidSalt(s string) bool { return saltPattern.MatchString(s) }

// Salts are an experiment's two salts.
type Salts struct{ Inclusion, Variation string }

// saltAttempts is how many candidates NewSalts draws before giving up. A
// salt is one of 2^64, so a collision with the project's handful of salts
// is vanishingly unlikely and two attempts would do; the margin costs
// nothing and keeps the loop honest.
const saltAttempts = 100

// ErrSaltsExhausted is what NewSalts answers when every candidate was taken.
var ErrSaltsExhausted = errors.New("experiments: every salt drawn was taken")

// NewSalts draws salts from next until it has two that differ from each
// other and from every one of taken: the salts the project's experiments and
// rollouts already use, so two assignments are never correlated. next
// returns a candidate in the form of a salt; its error is returned as it is.
func NewSalts(taken []string, next func() (string, error)) (Salts, error) {
	seen := make(map[string]bool, len(taken))
	for _, s := range taken {
		seen[s] = true
	}
	picked := make([]string, 0, 2)
	for attempt := 0; attempt < saltAttempts && len(picked) < 2; attempt++ {
		candidate, err := next()
		if err != nil {
			return Salts{}, err
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		picked = append(picked, candidate)
	}
	if len(picked) < 2 {
		return Salts{}, ErrSaltsExhausted
	}
	return Salts{Inclusion: picked[0], Variation: picked[1]}, nil
}
