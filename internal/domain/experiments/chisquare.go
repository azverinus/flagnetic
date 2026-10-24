package experiments

import "math"

// ChiSquarePValue is the probability that a chi-square variable with df
// degrees of freedom is at least x: the upper tail Q(df/2, x/2) that the
// uniformity check and the sample-ratio check both report. A statistic far
// beyond the tail answers 0, never NaN; with no degrees of freedom there is
// nothing to test and it answers 1.
func ChiSquarePValue(x float64, df int) float64 {
	if x <= 0 || df <= 0 {
		return 1
	}
	return regularizedGammaQ(float64(df)/2, x/2)
}

const (
	gammaEpsilon = 1e-15
	// gammaTiny keeps the continued fraction's denominators off zero.
	gammaTiny = 1e-300
)

// regularizedGammaQ is the regularised upper incomplete gamma function
// Q(a, x) = Γ(a, x) / Γ(a), for a > 0 and x > 0. Below x = a + 1 it is
// 1 − P(a, x) from P's power series, above it the continued fraction for Q
// evaluated by the modified Lentz method; each converges fast on its side.
func regularizedGammaQ(a, x float64) float64 {
	if x < a+1 {
		return 1 - lowerSeries(a, x)
	}
	return upperFraction(a, x)
}

// gammaIterations bounds both expansions. Near x = a either needs a few
// times √a terms to converge, so the bound grows with a: an independence
// test over 10,000 × 10,000 bins has about 10⁸ degrees of freedom, a = 5·10⁷,
// and a fixed bound of 10,000 terms would stop the series early and
// overstate the p-value.
func gammaIterations(a float64) int {
	return 10000 + int(20*math.Sqrt(a))
}

// gammaPrefix is x^a e^(−x) / Γ(a), computed in logarithms so that a large x
// underflows to 0 instead of overflowing.
func gammaPrefix(a, x float64) float64 {
	lg, _ := math.Lgamma(a)
	return math.Exp(a*math.Log(x) - x - lg)
}

// lowerSeries is P(a, x) = prefix · Σ x^n / (a (a+1) … (a+n)).
func lowerSeries(a, x float64) float64 {
	term := 1 / a
	sum := term
	for n, limit := 1, gammaIterations(a); n < limit; n++ {
		term *= x / (a + float64(n))
		sum += term
		if math.Abs(term) < math.Abs(sum)*gammaEpsilon {
			break
		}
	}
	return sum * gammaPrefix(a, x)
}

// upperFraction is Q(a, x) = prefix · 1/(x+1−a− 1·(1−a)/(x+3−a− 2·(2−a)/(x+5−a− …))).
func upperFraction(a, x float64) float64 {
	b := x + 1 - a
	c := 1 / gammaTiny
	d := 1 / b
	h := d
	for i, limit := 1, gammaIterations(a); i < limit; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < gammaTiny {
			d = gammaTiny
		}
		c = b + an/c
		if math.Abs(c) < gammaTiny {
			c = gammaTiny
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < gammaEpsilon {
			break
		}
	}
	return h * gammaPrefix(a, x)
}
