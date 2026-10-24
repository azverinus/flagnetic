package experiments

import (
	"maps"
	"slices"
	"strconv"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Bounds of a uniformity check.
const (
	MinUniformityUnitIDs  = 1000
	MaxUniformityUnitIDs  = 200000
	DefaultUniformityBins = 100
	minUniformityBins     = 2
	maxUniformitySalt     = 256
	// maxUnitIDViolations caps how many bad unit IDs a refusal names, so a
	// list of 200,000 bad IDs is not answered with 200,000 violations.
	maxUnitIDViolations = 100
	// uniformityThreshold is the p-value below which a test fails: the
	// sample-ratio check's threshold, low enough that among many honest
	// checks a false alarm stays rare.
	uniformityThreshold = 0.001
)

// Verdict is what a test of the uniformity check concludes.
type Verdict string

// The verdicts. An independence test whose table has a single non-empty row
// or column has no degrees of freedom and is untestable: every unit ID fell
// into one bin under one of the salts.
const (
	VerdictUniform     Verdict = "uniform"
	VerdictNotUniform  Verdict = "not_uniform"
	VerdictIndependent Verdict = "independent"
	VerdictDependent   Verdict = "dependent"
	VerdictUntestable  Verdict = "untestable"
)

// UniformityRequest is what a uniformity check runs on: real unit IDs, as
// often as each occurs, hashed with Salt and grouped into Bins bins.
type UniformityRequest struct {
	UnitIDs []string
	Salt    string
	// SecondSalt, when not nil, also tests that its buckets are independent
	// of Salt's, as an experiment's inclusion and variation buckets must be.
	SecondSalt *string
	Bins       int
}

// Uniformity is the outcome of a uniformity check.
type Uniformity struct {
	Salt       string
	SecondSalt *string
	Bins       int
	// Counts are how many unit IDs fell into each bin, in bin order.
	Counts    []int
	Min, Max  int
	ChiSquare float64
	DF        int
	PValue    float64
	Verdict   Verdict
	// Independence is nil without a second salt.
	Independence *Independence
}

// Independence is Pearson's test of independence on the two-way table of
// both salts' bins, over its non-empty rows and columns.
type Independence struct {
	ChiSquare float64
	DF        int
	PValue    float64
	Verdict   Verdict
}

// CheckUniformity checks how evenly the bucketing hash spreads r's unit IDs:
// the 10,000 buckets are grouped into r.Bins bins of consecutive buckets and
// each bin's count is compared with what a uniform hash expects of its width
// by Pearson's chi-square test. It is uniform when the p-value is at least
// 0.001. It records nothing and reads nothing but r.
func CheckUniformity(r UniformityRequest) (Uniformity, error) {
	if err := checkUniformityRequest(r); err != nil {
		return Uniformity{}, err
	}
	b := newBinning(r.Bins)
	first := b.of(r.Salt, r.UnitIDs)
	u := Uniformity{Salt: r.Salt, SecondSalt: r.SecondSalt, Bins: r.Bins, Counts: make([]int, r.Bins), DF: r.Bins - 1}
	for _, bin := range first {
		u.Counts[bin]++
	}
	u.Min, u.Max = slices.Min(u.Counts), slices.Max(u.Counts)
	n := float64(len(r.UnitIDs))
	for i, observed := range u.Counts {
		expected := n * float64(b.widths[i]) / eval.Buckets
		d := float64(observed) - expected
		u.ChiSquare += d * d / expected
	}
	u.PValue = ChiSquarePValue(u.ChiSquare, u.DF)
	u.Verdict = verdict(u.PValue, VerdictUniform, VerdictNotUniform)
	if r.SecondSalt != nil {
		u.Independence = independence(first, b.of(*r.SecondSalt, r.UnitIDs), r.Bins)
	}
	return u, nil
}

func checkUniformityRequest(r UniformityRequest) error {
	c := newCheck()
	if n := len(r.UnitIDs); n < MinUniformityUnitIDs || n > MaxUniformityUnitIDs {
		c.add("/unit_ids", "out_of_range", "a uniformity check takes 1,000 to 200,000 unit IDs",
			map[string]any{"min": MinUniformityUnitIDs, "max": MaxUniformityUnitIDs})
	} else {
		named := 0
		for i, id := range r.UnitIDs {
			if id != "" && len(id) <= flags.MaxUnitIDBytes {
				continue
			}
			c.add("/unit_ids/"+strconv.Itoa(i), "invalid_value", "a unit ID has 1 to 256 bytes",
				map[string]any{"min": 1, "max": flags.MaxUnitIDBytes})
			if named++; named == maxUnitIDViolations {
				break
			}
		}
	}
	if r.Bins < minUniformityBins || r.Bins > eval.Buckets {
		c.add("/bins", "out_of_range", "a uniformity check groups the buckets into 2 to 10,000 bins",
			map[string]any{"min": minUniformityBins, "max": eval.Buckets})
	}
	for _, s := range []struct {
		pointer string
		salt    *string
	}{{"/salt", &r.Salt}, {"/second_salt", r.SecondSalt}} {
		if s.salt != nil && (*s.salt == "" || len(*s.salt) > maxUniformitySalt) {
			c.add(s.pointer, "invalid_value", "a salt has 1 to 256 bytes", map[string]any{"min": 1, "max": maxUniformitySalt})
		}
	}
	if len(c.violations) == 0 {
		return nil
	}
	return apperrors.Invalid("invalid_uniformity_check", "the uniformity check cannot run on this request", c.violations...)
}

// binning groups the 10,000 buckets into consecutive bins: bucket b is in bin
// b × bins / 10000, so bins differ in width by at most one bucket.
type binning struct {
	bins   int
	widths []int
}

func newBinning(bins int) binning {
	b := binning{bins: bins, widths: make([]int, bins)}
	for bucket := range eval.Buckets {
		b.widths[b.bin(bucket)]++
	}
	return b
}

func (b binning) bin(bucket int) int { return bucket * b.bins / eval.Buckets }

// of returns the bin of every unit ID hashed with salt, in order.
func (b binning) of(salt string, unitIDs []string) []int {
	out := make([]int, len(unitIDs))
	for i, id := range unitIDs {
		out[i] = b.bin(eval.Bucket(salt, id))
	}
	return out
}

// independence is Pearson's test of independence on the two-way table of
// first and second bins. With E = row × column / N the statistic
// Σ (O − E)² / E equals N · Σ O² / (row · column) − N, a sum over the
// non-empty cells only, so a table of 10,000 × 10,000 bins costs no more
// than the unit IDs it holds.
func independence(first, second []int, bins int) *Independence {
	rows, cols := make([]int, bins), make([]int, bins)
	cells := map[int]int{}
	for i := range first {
		rows[first[i]]++
		cols[second[i]]++
		cells[first[i]*bins+second[i]]++
	}
	// In cell order, so that the same IDs give the same statistic to the
	// last digit whichever process sums them.
	sum := 0.0
	for _, cell := range slices.Sorted(maps.Keys(cells)) {
		o := float64(cells[cell])
		sum += o * o / (float64(rows[cell/bins]) * float64(cols[cell%bins]))
	}
	df := (nonEmpty(rows) - 1) * (nonEmpty(cols) - 1)
	if df == 0 {
		return &Independence{PValue: 1, Verdict: VerdictUntestable}
	}
	n := float64(len(first))
	chi := max(0, n*sum-n)
	p := ChiSquarePValue(chi, df)
	return &Independence{ChiSquare: chi, DF: df, PValue: p, Verdict: verdict(p, VerdictIndependent, VerdictDependent)}
}

func nonEmpty(counts []int) int {
	n := 0
	for _, c := range counts {
		if c > 0 {
			n++
		}
	}
	return n
}

func verdict(p float64, pass, fail Verdict) Verdict {
	if p >= uniformityThreshold {
		return pass
	}
	return fail
}
