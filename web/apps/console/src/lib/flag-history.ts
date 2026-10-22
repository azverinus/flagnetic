import type { AuditEntry, FlagConfiguration, FlagDefinition } from "@flagnetic/api";

/**
 * A flag's configuration as an audit entry records it: the whole document
 * with its version, which is what a revert writes again. An entry may
 * record more beside it, such as a kill switch's reason, which is ignored.
 */
export type RecordedConfiguration = Omit<FlagConfiguration, "environment" | "updated_at">;

/** A flag's definition as an audit entry records it. */
export type RecordedDefinition = Omit<
  FlagDefinition,
  "id" | "created_by" | "created_at" | "updated_at"
>;

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isConfiguration(value: unknown): value is RecordedConfiguration {
  return (
    isObject(value) &&
    typeof value.version === "number" &&
    typeof value.on === "boolean" &&
    Array.isArray(value.rules) &&
    Array.isArray(value.overrides) &&
    Array.isArray(value.prerequisites)
  );
}

function isDefinition(value: unknown): value is RecordedDefinition {
  return (
    isObject(value) &&
    typeof value.version === "number" &&
    typeof value.name === "string" &&
    Array.isArray(value.variations) &&
    // A flag created or deleted is recorded with every environment's
    // configuration as well, which is no change of its definition.
    !("environments" in value)
  );
}

export type Sides<T> = { before: T | undefined; after: T | undefined };

/**
 * The configuration before and after the change of a history entry made
 * in one environment, or undefined for an entry of the flag as a whole.
 */
export function configurationSides(entry: AuditEntry): Sides<RecordedConfiguration> | undefined {
  if (entry.environment_id === null) return undefined;
  const before = isConfiguration(entry.before) ? entry.before : undefined;
  const after = isConfiguration(entry.after) ? entry.after : undefined;
  return before || after ? { before, after } : undefined;
}

/**
 * The definition before and after a history entry that changed it, or
 * undefined for an entry of a configuration, or of the flag being created
 * or deleted.
 */
export function definitionSides(entry: AuditEntry): Sides<RecordedDefinition> | undefined {
  if (entry.environment_id !== null) return undefined;
  const before = isDefinition(entry.before) ? entry.before : undefined;
  const after = isDefinition(entry.after) ? entry.after : undefined;
  return before && after ? { before, after } : undefined;
}

/**
 * The version of its environment's configuration a history entry offers
 * to revert to: the one its change made, unless the configuration is at
 * that version still.
 */
export function revertVersion(entry: AuditEntry, current: number): number | undefined {
  const version = configurationSides(entry)?.after?.version;
  return version !== undefined && version !== current ? version : undefined;
}

/**
 * The version of its environment's configuration from before a history
 * entry's change, unless the configuration is at it now. The version a
 * configuration started at is made by creating its flag or environment,
 * whose entries record no configuration of their own, so the entry after
 * it is the only one offering a way back to it.
 */
export function versionBefore(entry: AuditEntry, current: number): number | undefined {
  const version = configurationSides(entry)?.before?.version;
  return version !== undefined && version !== current ? version : undefined;
}
