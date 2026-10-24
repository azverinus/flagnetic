package experiments_test

import (
	"math"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
)

// The upper tail of the chi-square distribution against tabulated values:
// the sample-ratio check's worked examples and the critical values printed in
// every statistics table.
func TestChiSquarePValueMatchesTabulatedValues(t *testing.T) {
	for _, c := range []struct {
		x    float64
		df   int
		want float64
		tol  float64 // relative
	}{
		// 5,200/4,800 and 5,050/4,950 against an even split.
		{x: 16, df: 1, want: 6.334248e-5, tol: 1e-5},
		{x: 1, df: 1, want: 0.3173105, tol: 1e-6},
		// Critical values.
		{x: 3.841459, df: 1, want: 0.05, tol: 1e-5},
		{x: 6.634897, df: 1, want: 0.01, tol: 1e-5},
		{x: 10.827566, df: 1, want: 0.001, tol: 1e-5},
		{x: 11.070498, df: 5, want: 0.05, tol: 1e-5},
		{x: 23.209251, df: 10, want: 0.01, tol: 1e-5},
		{x: 124.342113, df: 100, want: 0.05, tol: 1e-5},
		{x: 149.449252, df: 100, want: 0.001, tol: 1e-5},
		// Two degrees of freedom have a closed form, e^(−x/2).
		{x: 7, df: 2, want: math.Exp(-3.5), tol: 1e-12},
		{x: 0.5, df: 2, want: math.Exp(-0.25), tol: 1e-12},
	} {
		got := experiments.ChiSquarePValue(c.x, c.df)
		if math.Abs(got-c.want) > c.tol*c.want {
			t.Errorf("ChiSquarePValue(%g, %d) = %.9g, want %.9g", c.x, c.df, got, c.want)
		}
	}
}

func TestChiSquarePValueAtTheEdges(t *testing.T) {
	if got := experiments.ChiSquarePValue(0, 3); got != 1 {
		t.Errorf("at 0 = %g, want 1", got)
	}
	// No degrees of freedom leave nothing to test: nothing is unlikely.
	if got := experiments.ChiSquarePValue(5, 0); got != 1 {
		t.Errorf("with no degrees of freedom = %g, want 1", got)
	}
	// A statistic far beyond any table underflows to 0, never NaN.
	for _, x := range []float64{1e4, 1e7, 1e12} {
		if got := experiments.ChiSquarePValue(x, 99); got != 0 {
			t.Errorf("ChiSquarePValue(%g, 99) = %g, want 0", x, got)
		}
	}
	// Monotone: a larger statistic is never more likely.
	prev := 1.0
	for x := 0.0; x < 400; x += 0.5 {
		p := experiments.ChiSquarePValue(x, 99)
		if p > prev || p < 0 || math.IsNaN(p) {
			t.Fatalf("ChiSquarePValue(%g, 99) = %g after %g", x, p, prev)
		}
		prev = p
	}
}

// The upper tail is computed by a power series below x = df/2 + 1 and a
// continued fraction above it; closed forms on both sides show they agree and
// that the tail and its complement sum to one.
func TestChiSquarePValueAgreesAcrossBothMethods(t *testing.T) {
	// df = 2: Q(1, x/2) = e^(−x/2).
	for _, x := range []float64{0.2, 2, 4, 10, 60} {
		if got, want := experiments.ChiSquarePValue(x, 2), math.Exp(-x/2); math.Abs(got-want) > 1e-12 {
			t.Errorf("ChiSquarePValue(%g, 2) = %g, want %g", x, got, want)
		}
	}
	// df = 1: Q(1/2, x/2) = erfc(√(x/2)), and P = 1 − Q = erf(√(x/2)).
	for _, x := range []float64{0.02, 0.8, 3, 16, 80} {
		got, want := experiments.ChiSquarePValue(x, 1), math.Erfc(math.Sqrt(x/2))
		if math.Abs(got-want)/want > 1e-9 {
			t.Errorf("ChiSquarePValue(%g, 1) = %g, want %g", x, got, want)
		}
		if p := math.Erf(math.Sqrt(x / 2)); math.Abs(p+got-1) > 1e-12 {
			t.Errorf("P + Q at %g = %g, want 1", x, p+got)
		}
	}
}

// An independence test over many bins has up to about 10⁸ degrees of
// freedom; there the distribution is close to normal and the Wilson–Hilferty
// approximation is accurate to far better than the tolerance, on both sides
// of the mean.
func TestChiSquarePValueWithVeryManyDegreesOfFreedom(t *testing.T) {
	const df = 99_980_001
	for _, x := range []float64{99_960_000, 99_970_000, 99_980_000, 99_990_000, 100_000_000, 100_020_000} {
		k := float64(df)
		z := (math.Cbrt(x/k) - (1 - 2/(9*k))) / math.Sqrt(2/(9*k))
		want := 0.5 * math.Erfc(z/math.Sqrt2)
		if got := experiments.ChiSquarePValue(x, df); math.Abs(got-want) > 1e-4 {
			t.Errorf("ChiSquarePValue(%g, %d) = %.6g, want %.6g", x, df, got, want)
		}
	}
}
