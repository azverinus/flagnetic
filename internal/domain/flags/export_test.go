package flags

// MaxFlags and MaxSegments are the limits, for the tests of package flags_test.
const (
	MaxFlags    = maxFlags
	MaxSegments = maxSegments
)

// Proposable is proposable, for the tests of package flags_test.
func (c Configuration) Proposable(in ConfigurationInput) ConfigurationInput { return c.proposable(in) }
