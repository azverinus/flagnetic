package experiments_test

import (
	"errors"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
)

// source returns the candidates in order, then repeats the last one.
func source(candidates ...string) func() (string, error) {
	i := 0
	return func() (string, error) {
		if i < len(candidates) {
			i++
			return candidates[i-1], nil
		}
		return candidates[len(candidates)-1], nil
	}
}

func TestSaltsAreDistinctFromEachOtherAndFromEverySaltTheProjectHas(t *testing.T) {
	for _, c := range []struct {
		what       string
		taken      []string
		candidates []string
		want       experiments.Salts
	}{
		{"with nothing taken the first two draws are the salts",
			nil, []string{"a3f9c2e17b4d0e58", "7e1d4c90b2a6f3d1"},
			experiments.Salts{Inclusion: "a3f9c2e17b4d0e58", Variation: "7e1d4c90b2a6f3d1"}},
		{"a draw equal to a rollout's or another experiment's salt is skipped",
			[]string{"a3f9c2e17b4d0e58"},
			[]string{"a3f9c2e17b4d0e58", "7e1d4c90b2a6f3d1", "0011223344556677"},
			experiments.Salts{Inclusion: "7e1d4c90b2a6f3d1", Variation: "0011223344556677"}},
		{"the second salt never equals the first",
			nil, []string{"a3f9c2e17b4d0e58", "a3f9c2e17b4d0e58", "7e1d4c90b2a6f3d1"},
			experiments.Salts{Inclusion: "a3f9c2e17b4d0e58", Variation: "7e1d4c90b2a6f3d1"}},
	} {
		got, err := experiments.NewSalts(c.taken, source(c.candidates...))
		if err != nil || got != c.want {
			t.Errorf("%s: NewSalts = %+v, %v; want %+v", c.what, got, err, c.want)
		}
	}
}

func TestSaltsGiveUpWhenEveryCandidateIsTakenAndPassOnTheSourcesError(t *testing.T) {
	taken := []string{"a3f9c2e17b4d0e58"}
	if _, err := experiments.NewSalts(taken, source("a3f9c2e17b4d0e58")); !errors.Is(err, experiments.ErrSaltsExhausted) {
		t.Errorf("err = %v, want salts exhausted", err)
	}
	failed := errors.New("the reader failed")
	if _, err := experiments.NewSalts(nil, func() (string, error) { return "", failed }); !errors.Is(err, failed) {
		t.Errorf("err = %v, want the source's error", err)
	}
}

func TestOnlySixteenLowerCaseHexCharactersAreASalt(t *testing.T) {
	for s, want := range map[string]bool{
		"a3f9c2e17b4d0e58": true, "0000000000000000": true,
		"A3F9C2E17B4D0E58": false, "a3f9c2e17b4d0e5": false, "a3f9c2e17b4d0e581": false, "g3f9c2e17b4d0e58": false, "": false,
	} {
		if got := experiments.ValidSalt(s); got != want {
			t.Errorf("ValidSalt(%q) = %v, want %v", s, got, want)
		}
	}
}
