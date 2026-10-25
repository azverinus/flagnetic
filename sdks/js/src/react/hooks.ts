// The hooks. Each one subscribes through `useSyncExternalStore`, which is
// what makes them safe under concurrent rendering and under hydration: React
// reads the server snapshot while the page hydrates and only then looks at
// the live one, and it drops every subscription when the component unmounts,
// so nothing sets state on a component that is gone.
//
// A snapshot must keep the same identity while nothing changed, or React
// re-renders for ever. Each hook therefore holds the detail it last served
// and hands it back unless the value, the reason or where the fallback chain
// stands actually differ — a JSON flag, whose getter copies its value, would
// otherwise look different on every read.
import { useCallback, useContext, useRef, useSyncExternalStore } from "react";
import type { FlagneticClient } from "../client/client.js";
import { jsonEquals, type Detail, type JsonValue, type Stage } from "../client/types.js";
import { SourceContext } from "./provider.js";
import type { FlagneticSource } from "./source.js";

/**
 * The client the provider holds. A client the provider builds itself exists
 * only after hydration, so this is undefined while a page is rendered on a
 * server and hydrated in the browser; a client passed to the provider is
 * returned from the first render on. Reach for it to call `setContext`,
 * `exposure`, `alias`, `flush` or the local overrides.
 */
export function useFlagnetic(): FlagneticClient | undefined {
  const source = useSource();
  const subscribe = useCallback((notify: () => void) => source.subscribeClient(notify), [source]);
  const snapshot = useCallback(() => source.client(), [source]);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

/**
 * Where the fallback chain stands: `built_in`, `last_good` or `fresh`. It
 * follows every transition, so a page can tell "no values yet" from "these
 * are what the platform last served".
 */
export function useStage(): Stage {
  const source = useSource();
  const subscribe = useCallback((notify: () => void) => source.subscribeStage(notify), [source]);
  const snapshot = useCallback(() => source.stage(), [source]);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

/**
 * Serves a flag with why it was served, and re-renders this component — and
 * only the components reading this flag — whenever its value changes or the
 * fallback chain moves under it.
 *
 * The type of defaultValue names the flag type asked for: boolean, string,
 * number, or anything else as JSON. A missing flag, a type mismatch or stage
 * `built_in` serves defaultValue with reason `fallback`.
 */
export function useFlagDetail(key: string, defaultValue: boolean): Detail<boolean>;
export function useFlagDetail(key: string, defaultValue: string): Detail<string>;
export function useFlagDetail(key: string, defaultValue: number): Detail<number>;
export function useFlagDetail(key: string, defaultValue: JsonValue): Detail;
export function useFlagDetail(key: string, defaultValue: JsonValue): Detail {
  return useServed<Detail>(key, defaultValue, (detail) => detail, sameDetail);
}

/**
 * Serves a flag's value, and re-renders this component — and only the
 * components reading this flag — whenever it changes. The type of
 * defaultValue names the flag type asked for; {@link useFlagDetail} says why
 * the value was served.
 *
 * Only the value re-renders it: a stage transition, which {@link useStage}
 * and {@link useFlagDetail} report, leaves a value it does not change alone.
 */
export function useFlag(key: string, defaultValue: boolean): boolean;
export function useFlag(key: string, defaultValue: string): string;
export function useFlag(key: string, defaultValue: number): number;
export function useFlag(key: string, defaultValue: JsonValue): JsonValue;
export function useFlag(key: string, defaultValue: JsonValue): JsonValue {
  return useServed<JsonValue>(key, defaultValue, (detail) => detail.value, jsonEquals);
}

/**
 * What both flag hooks are: a subscription to one key, and a snapshot kept
 * stable while nothing a component could render differs.
 *
 * The cache lives in a ref written from `getSnapshot`, as React's own
 * `useSyncExternalStoreWithSelector` does: it is keyed on the key, the
 * default, the flag's version and what a detail carries beside its value —
 * the stage and the values behind it — so a render that is thrown away
 * leaves behind an entry the next one either reuses or replaces, never a
 * wrong answer.
 */
function useServed<T>(
  key: string,
  defaultValue: JsonValue,
  select: (detail: Detail) => T,
  same: (a: T, b: T) => boolean,
): T {
  const source = useSource();
  const held = useRef<{ key: string; def: JsonValue; version: number; metadata: string; served: T } | undefined>(undefined);

  const subscribe = useCallback((notify: () => void) => source.subscribeFlag(key, notify), [source, key]);
  const snapshot = useCallback((): T => {
    const version = source.flagVersion(key);
    const metadata = source.metadata();
    const last = held.current;
    // An evaluation is counted and a flag with automatic exposure records
    // its exposure, so the flag is read once per change, not once per render.
    if (last && last.version === version && last.metadata === metadata && last.key === key && jsonEquals(last.def, defaultValue)) {
      return last.served;
    }
    const next = select(source.getDetail(key, defaultValue));
    const served = last && same(last.served, next) ? last.served : next;
    held.current = { key, def: defaultValue, version, metadata, served };
    return served;
  }, [source, key, defaultValue, select, same]);

  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

function useSource(): FlagneticSource {
  const source = useContext(SourceContext);
  if (!source) throw new Error("flagnetic/react: this hook needs a <FlagneticProvider> above it");
  return source;
}

// Two details are the same when nothing a component could render differs.
function sameDetail(a: Detail, b: Detail): boolean {
  return (
    a.reason === b.reason &&
    a.variation === b.variation &&
    a.errorCode === b.errorCode &&
    a.stage === b.stage &&
    a.overridden === b.overridden &&
    a.rulesetVersion === b.rulesetVersion &&
    a.receivedAt?.getTime() === b.receivedAt?.getTime() &&
    a.experiment === b.experiment &&
    jsonEquals(a.value, b.value)
  );
}
