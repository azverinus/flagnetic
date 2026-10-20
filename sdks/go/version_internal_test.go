package flagnetic

import (
	"regexp"
	"testing"

	"github.com/azverinus/flagnetic/eval"
)

// The SDK declares the schema_version range it reads and the SDK API path it
// speaks; the compatibility page lists them per SDK line. A document outside
// the range is a fallback chain failure. The bounds are unexported, so this
// test reads them from inside the package.
func TestDeclaredSchemaRangeAndPath(t *testing.T) {
	if schemaVersionMin < 1 || schemaVersionMin > schemaVersionMax {
		t.Errorf("schema range %d–%d is empty", schemaVersionMin, schemaVersionMax)
	}
	// The evaluator compiles one schema; the SDK cannot read another.
	if schemaVersionMax != eval.SchemaVersion {
		t.Errorf("schemaVersionMax = %d, but the evaluator reads %d", schemaVersionMax, eval.SchemaVersion)
	}
	if !regexp.MustCompile(`^/sdk/v[1-9][0-9]*$`).MatchString(apiPath) {
		t.Errorf("apiPath = %q, want /sdk/vN", apiPath)
	}
}
