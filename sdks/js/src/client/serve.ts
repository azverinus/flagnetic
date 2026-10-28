// Serving one flag out of a values document. It is a pure function of the
// document, the local overrides and where the fallback chain stands, so the
// browser client and the React provider — which serves a bootstrap document
// on its own while a page is rendered and hydrated — cannot drift apart.
import { copyJson, fitsFlagType, type Detail, type ErrorCode, type FlagType, type JsonValue, type Stage } from "./types.js";
import type { ValuesDocument } from "./values.js";

/** What every detail carries whatever it serves: where the fallback chain stands. */
export interface ServeBase {
  readonly stage: Stage;
  readonly overridden: boolean;
  /** The ruleset version the held values were evaluated against; absent at stage `built_in`. */
  readonly rulesetVersion?: number;
  /** When the held values were received. */
  readonly receivedAt?: Date;
}

/** A value substituted inside this process, ahead of the values document. */
export interface LocalOverride {
  readonly type: FlagType;
  readonly value: JsonValue;
}

/**
 * Serves key as type, or def with reason `fallback` and an error code when
 * there is nothing of that type to serve. `onTypeMismatch` is called with
 * the flag's own type when a getter asked for another one, so the caller can
 * log it once per key.
 */
export function serveDetail<T extends JsonValue>(
  key: string,
  type: FlagType,
  def: T,
  values: ValuesDocument | undefined,
  local: LocalOverride | undefined,
  base: ServeBase,
  onTypeMismatch: (flagType: string) => void,
): Detail<T> {
  const fallback = (code: ErrorCode): Detail<T> => ({ ...base, value: def, reason: "fallback", errorCode: code });
  const mismatch = (flagType: string): Detail<T> => {
    onTypeMismatch(flagType);
    return fallback("type_mismatch");
  };
  if (local) {
    if (local.type !== type) return mismatch(local.type);
    // A JSON value is the caller's to mutate, so the override serves a copy
    // too: what one getter's caller changes never reaches the next.
    return { ...base, value: (type === "json" ? copyJson(local.value) : local.value) as T, reason: "local_override" };
  }
  if (!values) return fallback("no_values");
  const flag = values.flags.get(key);
  if (!flag) return fallback("flag_not_found");
  if (flag.type !== type) return mismatch(flag.type);
  if (flag.reason === "error") return fallback(flag.errorCode ?? "inconsistent_ruleset");
  if (!flag.hasValue || !fitsFlagType(type, flag.value)) return fallback("inconsistent_ruleset");
  return {
    ...base,
    value: (type === "json" ? copyJson(flag.value as JsonValue) : flag.value) as T,
    reason: flag.reason,
    ...(flag.variation !== undefined && { variation: flag.variation }),
    overridden: flag.overrideInfluenced,
    ...(flag.experiment && { experiment: flag.experiment }),
  };
}
