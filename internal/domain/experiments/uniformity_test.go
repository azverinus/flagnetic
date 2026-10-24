package experiments_test

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func unitIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("user-%d", i)
	}
	return out
}

func checkUniformity(t *testing.T, r experiments.UniformityRequest) experiments.Uniformity {
	t.Helper()
	u, err := experiments.CheckUniformity(r)
	if err != nil {
		t.Fatalf("CheckUniformity: %v", err)
	}
	return u
}

// A bin is a run of consecutive buckets: bucket b falls into bin
// b × bins / 10000, so the bins differ in width by at most one bucket.
func TestUniformityBinsConsecutiveBuckets(t *testing.T) {
	ids := unitIDs(5000)
	for _, bins := range []int{2, 3, 7, 100, 10000} {
		u := checkUniformity(t, experiments.UniformityRequest{UnitIDs: ids, Salt: "a1b2c3d4e5f60718", Bins: bins})
		want := make([]int, bins)
		for _, id := range ids {
			want[eval.Bucket("a1b2c3d4e5f60718", id)*bins/eval.Buckets]++
		}
		if !slices.Equal(u.Counts, want) {
			t.Errorf("bins %d: counts differ from the buckets' bins", bins)
		}
		if u.Bins != bins || u.DF != bins-1 || u.Min != slices.Min(want) || u.Max != slices.Max(want) {
			t.Errorf("bins %d: got bins %d, df %d, min %d, max %d", bins, u.Bins, u.DF, u.Min, u.Max)
		}
	}
}

// The statistic compares every bin with what a uniform hash expects of its
// width: with 3 bins the first holds 3,334 buckets and the others 3,333.
func TestUniformityExpectsEachBinsWidth(t *testing.T) {
	ids := unitIDs(3000)
	u := checkUniformity(t, experiments.UniformityRequest{UnitIDs: ids, Salt: "s", Bins: 3})
	widths := []float64{3334, 3333, 3333}
	chi := 0.0
	for i, o := range u.Counts {
		e := 3000 * widths[i] / eval.Buckets
		chi += (float64(o) - e) * (float64(o) - e) / e
	}
	if math.Abs(u.ChiSquare-chi) > 1e-9 {
		t.Errorf("chi-square = %g, want %g", u.ChiSquare, chi)
	}
	if u.PValue != experiments.ChiSquarePValue(chi, 2) {
		t.Errorf("p-value = %g, want the upper tail of %g with 2 degrees of freedom", u.PValue, chi)
	}
}

func TestUniformityVerdict(t *testing.T) {
	u := checkUniformity(t, experiments.UniformityRequest{UnitIDs: unitIDs(20000), Salt: "a1b2c3d4e5f60718", Bins: 100})
	if u.Verdict != experiments.VerdictUniform || u.PValue < 0.001 {
		t.Errorf("distinct IDs: verdict %s at p = %g, want uniform", u.Verdict, u.PValue)
	}
	if u.Independence != nil {
		t.Error("without a second salt there is no independence test")
	}
	// Duplicates count as often as they occur, so one ID repeated piles
	// into one bin.
	same := slices.Repeat([]string{"user-1"}, 1000)
	u = checkUniformity(t, experiments.UniformityRequest{UnitIDs: same, Salt: "a1b2c3d4e5f60718", Bins: 100})
	if u.Verdict != experiments.VerdictNotUniform || u.Max != 1000 || u.Min != 0 {
		t.Errorf("one repeated ID: verdict %s, min %d, max %d", u.Verdict, u.Min, u.Max)
	}
}

func TestUniformityIndependence(t *testing.T) {
	ids := unitIDs(20000)
	second := "0f1e2d3c4b5a6978"
	u := checkUniformity(t, experiments.UniformityRequest{UnitIDs: ids, Salt: "a1b2c3d4e5f60718", SecondSalt: &second, Bins: 10})
	if u.Independence == nil || u.Independence.Verdict != experiments.VerdictIndependent || u.Independence.DF != 81 {
		t.Fatalf("two salts: %+v, want independent with 81 degrees of freedom", u.Independence)
	}
	if *u.SecondSalt != second {
		t.Errorf("second salt = %q", *u.SecondSalt)
	}

	// The same salt twice puts every ID on the table's diagonal.
	same := "a1b2c3d4e5f60718"
	u = checkUniformity(t, experiments.UniformityRequest{UnitIDs: ids, Salt: same, SecondSalt: &same, Bins: 10})
	if u.Independence.Verdict != experiments.VerdictDependent || u.Independence.PValue >= 0.001 {
		t.Errorf("one salt twice: %+v, want dependent", u.Independence)
	}

	// Every ID in one bin under both salts leaves no degrees of freedom.
	repeated := slices.Repeat([]string{"user-1"}, 1000)
	u = checkUniformity(t, experiments.UniformityRequest{UnitIDs: repeated, Salt: same, SecondSalt: &second, Bins: 10})
	if i := u.Independence; i.Verdict != experiments.VerdictUntestable || i.DF != 0 || i.ChiSquare != 0 {
		t.Errorf("one repeated ID: %+v, want untestable", i)
	}
}

// The independence statistic sums over the table's non-empty cells only;
// it equals Pearson's Σ (O − E)² / E with E = row × column / N over the
// non-empty rows and columns.
func TestUniformityIndependenceIsPearsonsStatistic(t *testing.T) {
	ids := unitIDs(2000)
	first, second, bins := "a1b2c3d4e5f60718", "0f1e2d3c4b5a6978", 4
	u := checkUniformity(t, experiments.UniformityRequest{UnitIDs: ids, Salt: first, SecondSalt: &second, Bins: bins})

	table := make([][]float64, bins)
	for i := range table {
		table[i] = make([]float64, bins)
	}
	rows, cols := make([]float64, bins), make([]float64, bins)
	for _, id := range ids {
		r, c := eval.Bucket(first, id)*bins/eval.Buckets, eval.Bucket(second, id)*bins/eval.Buckets
		table[r][c]++
		rows[r]++
		cols[c]++
	}
	chi := 0.0
	for r := range bins {
		for c := range bins {
			e := rows[r] * cols[c] / float64(len(ids))
			chi += (table[r][c] - e) * (table[r][c] - e) / e
		}
	}
	if math.Abs(u.Independence.ChiSquare-chi) > 1e-9*chi {
		t.Errorf("independence chi-square = %g, want %g", u.Independence.ChiSquare, chi)
	}
	if u.Independence.DF != 9 {
		t.Errorf("df = %d, want 9", u.Independence.DF)
	}
}

func TestUniformityRefusesWhatItCannotCheck(t *testing.T) {
	ok := unitIDs(1000)
	long := strings.Repeat("x", 257)
	empty := ""
	withBad := slices.Clone(ok)
	withBad[3], withBad[7] = "", long

	for _, c := range []struct {
		name string
		req  experiments.UniformityRequest
		want []string
	}{
		{"too few", experiments.UniformityRequest{UnitIDs: unitIDs(999), Salt: "s", Bins: 100},
			[]string{"/unit_ids out_of_range"}},
		{"too many", experiments.UniformityRequest{UnitIDs: unitIDs(200001), Salt: "s", Bins: 100},
			[]string{"/unit_ids out_of_range"}},
		{"bad unit IDs", experiments.UniformityRequest{UnitIDs: withBad, Salt: "s", Bins: 100},
			[]string{"/unit_ids/3 invalid_value", "/unit_ids/7 invalid_value"}},
		{"bins", experiments.UniformityRequest{UnitIDs: ok, Salt: "s", Bins: 1},
			[]string{"/bins out_of_range"}},
		{"bins over", experiments.UniformityRequest{UnitIDs: ok, Salt: "s", Bins: 10001},
			[]string{"/bins out_of_range"}},
		{"salts", experiments.UniformityRequest{UnitIDs: ok, Salt: long, SecondSalt: &empty, Bins: 100},
			[]string{"/salt invalid_value", "/second_salt invalid_value"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := experiments.CheckUniformity(c.req)
			var e *apperrors.Error
			if !errors.As(err, &e) || e.Kind != apperrors.KindInvalid || e.Code != "invalid_uniformity_check" {
				t.Fatalf("err = %v, want invalid_uniformity_check", err)
			}
			got := make([]string, 0, len(e.Violations))
			for _, v := range e.Violations {
				got = append(got, v.Pointer+" "+v.Code)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("violations = %v, want %v", got, c.want)
			}
		})
	}
}

// A list full of bad IDs names only the first few, so a refusal stays small
// whatever was sent.
func TestUniformityNamesAtMostTheFirstBadUnitIDs(t *testing.T) {
	_, err := experiments.CheckUniformity(experiments.UniformityRequest{UnitIDs: make([]string, 5000), Salt: "s", Bins: 100})
	var e *apperrors.Error
	if !errors.As(err, &e) || len(e.Violations) != 100 || e.Violations[99].Pointer != "/unit_ids/99" {
		t.Fatalf("err = %v", err)
	}
}
