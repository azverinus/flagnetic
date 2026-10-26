import type { Experiment, ExperimentAlert, ExperimentAlertKind } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/**
 * Whether an experiment runs past its expected end: from the UTC day after
 * the `expected_end_at` date, the rule the list's `past_expected_end`
 * filter and alert follow, so the marker and the filter agree.
 */
export function isOverdue(
  experiment: Pick<Experiment, "status" | "expected_end_at">,
  now: Date,
): boolean {
  if (experiment.status !== "running" || experiment.expected_end_at === null) return false;
  return now.toISOString().slice(0, 10) > experiment.expected_end_at;
}

/**
 * The active alerts nobody has acknowledged yet: those shown as banners
 * and counted by the list's marker. An acknowledgement hides an alert
 * until it clears; a condition returning raises a new one.
 */
export function awaitingAcknowledgement(alerts: readonly ExperimentAlert[]): ExperimentAlert[] {
  return alerts.filter((alert) => alert.cleared_at === null && alert.acknowledged_at === null);
}

/** What a kind of alert is called. */
export function alertKindName(i18n: I18n, kind: ExperimentAlertKind): string {
  switch (kind) {
    case "srm":
      return i18n._(msg`Sample-ratio mismatch`);
    case "no_exposures":
      return i18n._(msg`No exposures`);
    case "exposure_conflicts":
      return i18n._(msg`Exposure conflicts`);
    case "past_expected_end":
      return i18n._(msg`Past expected end`);
  }
}

/** Below it a p-value is shown as a bound: its digits say nothing more. */
const smallestShown = 0.0001;

/** A p-value to two significant digits in the member's language, the smallest as a bound. */
export function pValueText(i18n: I18n, p: number): string {
  if (p < smallestShown) {
    const bound = new Intl.NumberFormat(i18n.locale, { maximumSignificantDigits: 2 }).format(
      smallestShown,
    );
    return i18n._(msg`< ${bound}`);
  }
  return new Intl.NumberFormat(i18n.locale, { maximumSignificantDigits: 2 }).format(p);
}
