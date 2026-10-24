// The errors `overrides.set` throws in every mode, so a caller handles a
// refused local override the same way whichever client it holds.

/** Thrown by `overrides.set` on a client built without the `allowOverrides` option. */
export class LocalOverridesNotAllowed extends Error {
  constructor() {
    super("flagnetic: local overrides need the allowOverrides option");
    this.name = "LocalOverridesNotAllowed";
  }
}

/** Thrown by `overrides.set` while the held ruleset or values belong to a protected environment. */
export class LocalOverrideRefused extends Error {
  constructor(key: string) {
    super(`flagnetic: local override of ${JSON.stringify(key)} refused in a protected environment`);
    this.name = "LocalOverrideRefused";
  }
}
