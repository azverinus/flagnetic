// What an SDK sends to POST /sdk/v1/events: the envelope's limits, the
// exposure and alias fields and the counting every
// evaluation does before an evaluation summary is built from it. Server
// mode queues in memory and reports summaries on their own interval, client
// mode persists its queue and lets the summary ride along; what an event is
// does not differ between them, so it lives here.
import type { Experiment } from "./detail.js";
import { uuidV7 } from "./uuid.js";

/** One queued event, in the shape the events endpoint takes. */
export type QueuedEvent = Record<string, unknown> & { readonly type: string; readonly event_id: string };

/**
 * Limits of POST /sdk/v1/events. The body limit is on the
 * decompressed envelope, so a batch is packed against it before gzipping.
 */
export const MAX_EVENTS_PER_BATCH = 500;
export const MAX_SUMMARY_ROWS = 1_000;
export const MAX_BATCH_BYTES = 1 << 20;

/** The key a summary row is counted under: one flag, one variation, one reason. */
export function countKey(flag: string, variation: string | null, reason: string): string {
  return JSON.stringify([flag, variation, reason]);
}

/** One row of an evaluation summary: no unit IDs, no values. */
export interface SummaryRow<R extends string = string> {
  readonly flag: string;
  /** Null when no variation was served (`fallback`, `error`, `local_override`). */
  readonly variation: string | null;
  readonly reason: R;
  readonly count: number;
}

/** Counts evaluations per (flag, variation, reason) for one interval. */
export class SummaryCounter<R extends string = string> {
  private rows = new Map<string, { flag: string; variation: string | null; reason: R; count: number }>();
  private start: number;

  constructor(private readonly now: () => number) {
    this.start = now();
  }

  get size(): number {
    return this.rows.size;
  }

  count(flag: string, variation: string | undefined, reason: R): void {
    const v = variation ?? null;
    const id = countKey(flag, v, reason);
    const row = this.rows.get(id);
    if (row) row.count++;
    else this.rows.set(id, { flag, variation: v, reason, count: 1 });
  }

  /** The counts since construction or the last take, ordered by flag, variation and reason. */
  counts(): SummaryRow<R>[] {
    return orderRows([...this.rows.values()]);
  }

  /** The counts and the interval they cover, resetting the counter. */
  take(): { counts: SummaryRow<R>[]; intervalStart: number; intervalEnd: number } {
    const taken = { counts: this.counts(), intervalStart: this.start, intervalEnd: this.now() };
    this.rows = new Map();
    this.start = taken.intervalEnd;
    return taken;
  }
}

/** Orders summary rows by flag, variation and reason, so a batch reads the same every time. */
export function orderRows<R extends string>(rows: readonly SummaryRow<R>[]): SummaryRow<R>[] {
  const cmp = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0);
  return [...rows].sort((a, b) => cmp(a.flag, b.flag) || cmp(a.variation ?? "", b.variation ?? "") || cmp(a.reason, b.reason));
}

/** One unit's assignment in one experiment: what a repeated exposure is repeated about. */
export interface Assignment {
  readonly key: string;
  readonly variation: string;
  readonly unitType: string;
  readonly unitId: string;
}

/** The assignment an evaluation served, which is what its exposure reports. */
export interface ServedAssignment {
  readonly experiment: Experiment;
  /** The evaluation reason the detail carried: `experiment`, or `override`. */
  readonly reason: string;
  readonly rulesetVersion: number;
}

/**
 * The most recent assignment every flag served, by flag and by the context
 * it was evaluated for: what an exposure call reports. Reporting a
 * fresh evaluation instead would report an assignment the application never
 * served, or nothing at all, when a ruleset arrives between the evaluation
 * and the call.
 *
 * The memory is bounded and drops its oldest entry first, so a service
 * evaluating for endless contexts keeps the assignments most likely to be
 * reported; a forgotten one costs an exposure the application asked for,
 * which is why the bound is generous.
 */
export class ServedDetails {
  private readonly held = new Map<string, ServedAssignment>();
  /** The flags an evaluation of which has ever carried an experiment block. */
  private readonly everExperiment = new Set<string>();

  constructor(private readonly capacity: number) {}

  /** Remembers what flag served for context, unless a later ruleset is already held. */
  remember(flag: string, context: string, served: ServedAssignment): void {
    this.everExperiment.add(flag);
    const id = key(flag, context);
    const previous = this.held.get(id);
    if (previous && previous.rulesetVersion > served.rulesetVersion) return;
    // A Map iterates in insertion order, so the first entry is the oldest.
    this.held.delete(id);
    while (this.held.size >= this.capacity) {
      const [oldest] = this.held.keys();
      if (oldest === undefined) break;
      this.held.delete(oldest);
    }
    this.held.set(id, served);
  }

  /**
   * Forgets what flag served for context, because its evaluation no longer
   * carries an experiment block — unless what is held came from a later
   * ruleset than the evaluation that found none.
   */
  forget(flag: string, context: string, rulesetVersion: number): void {
    if (!this.everExperiment.has(flag)) return;
    const id = key(flag, context);
    const previous = this.held.get(id);
    if (previous && previous.rulesetVersion > rulesetVersion) return;
    this.held.delete(id);
  }

  /** What flag served for context, or undefined. */
  recall(flag: string, context: string): ServedAssignment | undefined {
    return this.held.get(key(flag, context));
  }

  /**
   * Whether an evaluation of flag has ever carried an experiment block. It
   * tells the two mistakes behind an exposure call with nothing to report
   * apart: a flag with no running experiment or a wrong key, and a call
   * passing a context the flag was not evaluated with.
   */
  everCarried(flag: string): boolean {
    return this.everExperiment.has(flag);
  }
}

/**
 * A context's fingerprint: the units and attributes it carries, so that an
 * exposure call finds the evaluation made for the same context however the
 * object was built. It names a memory entry and nothing else.
 */
export function contextFingerprint(context: unknown): string {
  const c = (typeof context === "object" && context !== null ? context : {}) as {
    units?: Readonly<Record<string, unknown>>;
    attributes?: Readonly<Record<string, unknown>>;
  };
  return JSON.stringify([entries(c.units), entries(c.attributes)]);
}

function entries(of: unknown): [string, unknown][] {
  if (typeof of !== "object" || of === null) return [];
  const record = of as Record<string, unknown>;
  return Object.keys(record)
    .sort()
    .map((k) => [k, record[k]]);
}

function key(flag: string, context: string): string {
  return `${flag}\u0000${context}`;
}

/**
 * The bounded in-process set that suppresses repeats of one assignment. At capacity the oldest assignment is forgotten rather than all
 * of them, so the most recent ones keep being suppressed; the platform's
 * deduplication by event ID and its first exposure stay authoritative, so a
 * forgotten one costs at most a duplicate the platform drops.
 */
export class ExposureSuppressor {
  private readonly seen = new Set<string>();

  constructor(private readonly capacity: number) {}

  /** True the first time this (experiment, unit type, unit ID, variation) is seen. */
  admit(a: Assignment): boolean {
    const id = JSON.stringify([a.key, a.unitType, a.unitId, a.variation]);
    if (this.seen.has(id)) return false;
    // A Set iterates in insertion order, so the first entry is the oldest.
    while (this.seen.size >= this.capacity) {
      const [oldest] = this.seen;
      if (oldest === undefined) break;
      this.seen.delete(oldest);
    }
    this.seen.add(id);
    return true;
  }
}

/**
 * The evaluation reason an exposure reports: the experiment block
 * is present with reason `experiment`, and with reason `override` when an
 * override's shadow evaluation lands in a running experiment.
 */
export type ExposureReason = "experiment" | "override";

/** Everything an exposure reports about the evaluation that produced it. */
export interface ExposureFields {
  readonly flagKey: string;
  readonly experimentKey: string;
  readonly variationKey: string;
  readonly unitType: string;
  readonly unitId: string;
  readonly reason: ExposureReason;
  readonly rulesetVersion: number;
  /** The experiment block's `override`: such exposures are excluded from analysis. */
  readonly override: boolean;
  readonly platform?: string;
  readonly appVersion?: string;
}

/**
 * One exposure event. The event ID is generated here, when the exposure is
 * recorded, so every retry of the batch carries the same one and the
 * platform drops it as a duplicate. SDK name and version travel in the
 * envelope; environment and `received_at` are the platform's.
 */
export function exposureEvent(at: number, random: () => number, f: ExposureFields): QueuedEvent {
  return {
    type: "exposure",
    event_id: uuidV7(at, random),
    occurred_at: new Date(at).toISOString(),
    experiment_key: f.experimentKey,
    flag_key: f.flagKey,
    variation_key: f.variationKey,
    unit_type: f.unitType,
    unit_id: f.unitId,
    ...(f.platform !== undefined && { platform: f.platform }),
    ...(f.appVersion !== undefined && { app_version: f.appVersion }),
    evaluation_reason: f.reason,
    ruleset_version: f.rulesetVersion,
    override: f.override,
  };
}

/** The two unit IDs an alias links, typically an anonymous ID and a user ID. */
export interface AliasLink {
  readonly fromUnitType: string;
  readonly fromUnitId: string;
  readonly toUnitType: string;
  readonly toUnitId: string;
}

/** One alias event; it changes no context. */
export function aliasEvent(at: number, random: () => number, link: AliasLink): QueuedEvent {
  return {
    type: "alias",
    event_id: uuidV7(at, random),
    occurred_at: new Date(at).toISOString(),
    from_unit_type: link.fromUnitType,
    from_unit_id: link.fromUnitId,
    to_unit_type: link.toUnitType,
    to_unit_id: link.toUnitId,
  };
}

/** What one evaluation summary reports besides its counts. */
export interface SummaryWindow {
  readonly intervalStart: number;
  readonly intervalEnd: number;
  /** The ruleset version held at the interval's end; 0 when none is. */
  readonly rulesetVersion: number;
  readonly appVersion?: string;
}

/**
 * The evaluation summaries one window becomes: its rows in order, at most
 * 1000 to an event, each with its own event ID and all naming the window's
 * interval and the ruleset version held at its end. No rows, no event.
 */
export function summaryEvents(at: number, random: () => number, w: SummaryWindow, counts: readonly SummaryRow[]): QueuedEvent[] {
  const events: QueuedEvent[] = [];
  for (let from = 0; from < counts.length; from += MAX_SUMMARY_ROWS) {
    events.push({
      type: "evaluation_summary",
      event_id: uuidV7(at, random),
      interval_start: new Date(w.intervalStart).toISOString(),
      interval_end: new Date(w.intervalEnd).toISOString(),
      ruleset_version: w.rulesetVersion,
      ...(w.appVersion !== undefined && { app_version: w.appVersion }),
      counts: counts.slice(from, from + MAX_SUMMARY_ROWS),
    });
  }
  return events;
}

/**
 * How many bytes a string takes as UTF-8, which is what the endpoint's 1 MiB
 * limit counts; `String.length` counts UTF-16 units and would under-count a
 * unit ID outside the Basic Latin block.
 */
export function utf8Length(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c < 0xdc00) {
      // A surrogate pair is one code point of four bytes.
      n += 4;
      i++;
    } else n += 3;
  }
  return n;
}

/**
 * Splits events into batches the endpoint accepts: at most 500 events and,
 * decompressed, under 1 MiB counting the envelope. An event too large on
 * its own still becomes a batch of one, which the platform answers 413 and
 * the caller drops.
 */
export function packBatches(events: readonly QueuedEvent[], envelopeOverhead: number): QueuedEvent[][] {
  const budget = MAX_BATCH_BYTES - envelopeOverhead;
  const batches: QueuedEvent[][] = [];
  let current: QueuedEvent[] = [];
  let size = 0;
  for (const e of events) {
    // One comma per event after the first.
    const cost = utf8Length(JSON.stringify(e)) + 1;
    if (current.length > 0 && (current.length === MAX_EVENTS_PER_BATCH || size + cost > budget)) {
      batches.push(current);
      current = [];
      size = 0;
    }
    current.push(e);
    size += cost;
  }
  if (current.length > 0) batches.push(current);
  return batches;
}
