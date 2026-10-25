// Reads the details of an OpenFeature `track` call for the two tracking
// events every Flagnetic provider accepts: `exposure` and `alias`.
import type { AliasLink } from "../internal/events.js";

/** The shape this module reads of OpenFeature `TrackingEventDetails`. */
export type TrackingDetailsLike = Readonly<Record<string, unknown>> | undefined;

/** The flag `track("exposure", context, { flag })` names, if it names one. */
export function trackedFlag(details: TrackingDetailsLike): string | undefined {
  return present(details?.["flag"]);
}

/** The alias `track("alias", context, { fromUnitType, fromUnitId, toUnitType, toUnitId })` names, if every unit is named. */
export function trackedAlias(details: TrackingDetailsLike): AliasLink | undefined {
  const fromUnitType = present(details?.["fromUnitType"]);
  const fromUnitId = present(details?.["fromUnitId"]);
  const toUnitType = present(details?.["toUnitType"]);
  const toUnitId = present(details?.["toUnitId"]);
  if (fromUnitType === undefined || fromUnitId === undefined || toUnitType === undefined || toUnitId === undefined) return undefined;
  return { fromUnitType, fromUnitId, toUnitType, toUnitId };
}

function present(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}
