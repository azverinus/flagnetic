/** The package version, reported in the User-Agent. Kept equal to package.json by a test. */
export const VERSION = "0.0.0";

/**
 * The lowest `schema_version` of the documents this SDK reads. A document outside
 * `SCHEMA_VERSION_MIN`–`SCHEMA_VERSION_MAX` is a fallback chain failure,
 * logged once as `schema_unsupported`. A change to the range or to
 * `SDK_API_PATH` needs a row in docs/sdks/compatibility.md.
 */
export const SCHEMA_VERSION_MIN = 1;

/** The highest `schema_version` this SDK reads, asked for in `Flagnetic-Schema-Version` by every evaluate, ruleset and events request. */
export const SCHEMA_VERSION_MAX = 1;

/** The SDK API path this SDK speaks. A new path is a new major version. */
export const SDK_API_PATH = "/sdk/v1";

/** Every `schema_version` from `SCHEMA_VERSION_MIN` to `SCHEMA_VERSION_MAX`, as `schema_unsupported` reports them. */
export const SUPPORTED_SCHEMA_VERSIONS: readonly number[] = Array.from(
  { length: SCHEMA_VERSION_MAX - SCHEMA_VERSION_MIN + 1 },
  (_, i) => SCHEMA_VERSION_MIN + i,
);
