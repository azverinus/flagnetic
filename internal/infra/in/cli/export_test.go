package cli

// The options below exist for tests alone: they replace what the binary reads
// from its build or its database, so a test can produce a state a real
// deployment cannot be put in on demand.
var (
	WithMigrations       = withMigrations
	WithStdin            = withStdin
	WithServerVersionNum = withServerVersionNum
	WithRoute            = withRoute
	WithWorker           = withWorker
)
