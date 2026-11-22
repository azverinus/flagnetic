import type { Context } from "../evaluator/evaluate.js";

/** The evaluations the provider remembers; the oldest goes out first. */
export const LAST_CONTEXT_CAPACITY = 10_000;

/**
 * The Flagnetic context the provider last evaluated an experiment flag with,
 * per flag and units. OpenFeature runs no hooks for `track`, so the context
 * `track` receives lacks what a `before` hook added to the evaluation, and
 * the client would find no served detail under it; the remembered context is
 * the one the detail is filed under.
 */
export class LastContexts {
  private readonly byKey = new Map<string, Context>();

  remember(flag: string, context: Context): void {
    const key = keyOf(flag, context);
    // Re-inserting moves the entry to the newest end.
    this.byKey.delete(key);
    this.byKey.set(key, context);
    if (this.byKey.size > LAST_CONTEXT_CAPACITY) {
      const oldest = this.byKey.keys().next();
      if (oldest.done !== true) this.byKey.delete(oldest.value);
    }
  }

  /** Drops what was remembered, so a flag that no longer carries an experiment block falls back to the tracked context. */
  forget(flag: string, context: Context): void {
    this.byKey.delete(keyOf(flag, context));
  }

  /** The context the flag was last evaluated with for these units, or `fallback`. */
  recall(flag: string, fallback: Context): Context {
    return this.byKey.get(keyOf(flag, fallback)) ?? fallback;
  }

  get size(): number {
    return this.byKey.size;
  }
}

function keyOf(flag: string, context: Context): string {
  const units = Object.entries(context.units ?? {}).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return JSON.stringify([flag, units]);
}
