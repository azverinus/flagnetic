package flagnetic

// Version is the version of this SDK, sent in every request's User-Agent.
// release-please keeps it equal to the module's tag through the marker
// below; 0.0.0 is a build from the repository before the first release.
const Version = "0.0.0" // x-release-please-version

// The schema_version range of the documents this SDK reads and the SDK API
// path it speaks. A ruleset outside the range is a fallback chain failure,
// logged once as schema_unsupported; every ruleset and events request asks
// for schemaVersionMax in Flagnetic-Schema-Version (the change stream
// carries no document). A change to any of these is a change of the
// compatibility the SDK documents, and a new apiPath is a new major
// Version.
const (
	schemaVersionMin = 1
	schemaVersionMax = 1
	apiPath          = "/sdk/v1"
)
