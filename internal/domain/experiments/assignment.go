package experiments

import (
	"strconv"
	"strings"

	"github.com/azverinus/flagnetic/eval"
)

// Assignment is how an experiment treats one unit, from the experiment's own
// fields: what an analyst needs to answer a support question and to recompute
// the answer in a warehouse.
type Assignment struct {
	UnitType string
	UnitID   string
	// InclusionSalt and VariationSalt are the experiment's salts, hashed
	// into InclusionBucket and VariationBucket. The variation bucket is given
	// for a unit left out too: it is the bucket the unit would be assigned by
	// should the allocation grow to include it.
	InclusionSalt     string
	VariationSalt     string
	InclusionBucket   int
	VariationBucket   int
	TrafficAllocation int
	// Included is whether the inclusion bucket is below the traffic
	// allocation.
	Included bool
	// Variation is the variation an included unit is assigned; nil when the
	// unit is not included.
	Variation *string
	// SQL is a ClickHouse query recomputing all of the above for the unit;
	// its subquery stands for any table with a unit_id column.
	SQL string
}

// Assign answers how e treats unitID in whatever status it is, with the
// evaluator's own hash: included when the inclusion bucket is below the
// traffic allocation, then the variation whose cumulative range of weights,
// in the experiment's order, holds the variation bucket.
func (e Experiment) Assign(unitID string) Assignment {
	a := Assignment{
		UnitType: e.UnitType, UnitID: unitID,
		InclusionSalt: e.InclusionSalt, VariationSalt: e.VariationSalt,
		InclusionBucket: eval.Bucket(e.InclusionSalt, unitID), VariationBucket: eval.Bucket(e.VariationSalt, unitID),
		TrafficAllocation: e.TrafficAllocation,
		SQL:               e.assignmentSQL(unitID),
	}
	a.Included = a.InclusionBucket < e.TrafficAllocation
	if !a.Included {
		return a
	}
	upper := 0
	for _, w := range e.Variations {
		upper += w.Weight
		if a.VariationBucket < upper {
			a.Variation = &w.Variation
			break
		}
	}
	return a
}

// assignmentSQL is the ClickHouse query recomputing unitID's assignment:
// both buckets as murmurHash3_32(concat(salt, '.', unit_id)) % 10000, the
// inclusion against the allocation and the variation walk over the
// cumulative weights. A variation weighing nothing covers no bucket and is
// left out of the walk.
func (e Experiment) assignmentSQL(unitID string) string {
	bucket := func(salt string) string {
		return "murmurHash3_32(concat(" + sqlString(salt) + ", '.', unit_id)) % " + strconv.Itoa(eval.Buckets)
	}
	var walk strings.Builder
	upper := 0
	for _, w := range e.Variations {
		if w.Weight == 0 {
			continue
		}
		upper += w.Weight
		walk.WriteString("variation_bucket < " + strconv.Itoa(upper) + ", " + sqlString(w.Variation) + ", ")
	}
	return "SELECT\n" +
		"    unit_id,\n" +
		"    " + bucket(e.InclusionSalt) + " AS inclusion_bucket,\n" +
		"    " + bucket(e.VariationSalt) + " AS variation_bucket,\n" +
		"    inclusion_bucket < " + strconv.Itoa(e.TrafficAllocation) + " AS included,\n" +
		"    if(included, multiIf(" + walk.String() + "NULL), NULL) AS variation\n" +
		"FROM (SELECT " + sqlString(unitID) + " AS unit_id)"
}

// sqlString is s as a ClickHouse string literal: quoted, with backslashes
// and quotes escaped.
func sqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
