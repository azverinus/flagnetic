package flagnetic

// Version is the version of this SDK, sent in every request's User-Agent.
const Version = "0.1.0"

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
