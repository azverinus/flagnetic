/**
 * `flagnetic/react`: the provider and the hooks over the browser client.
 * `react` is an optional peer dependency (every other entry point loads
 * without it), and this one holds no evaluator: a browser asks the server to
 * evaluate. Build a client with `createClient` from `flagnetic/client`, or let
 * the provider build one.
 *
 * A page rendered on a server hands the values document the server mode
 * produced (`flagnetic/server`'s `values`) to the provider as `bootstrap`:
 * the hooks serve it while the page renders and hydrates, so the first
 * render in the browser is the one the server sent, and the provider's own
 * client takes over, and refetches, after hydration.
 */
export { FlagneticProvider, type FlagneticProviderProps } from "./react/provider.js";
export { useFlag, useFlagDetail, useFlagnetic, useStage } from "./react/hooks.js";
export type { FlagneticClient } from "./client/client.js";
export type { ClientOptions, Context, Detail, JsonValue, Reason, Stage, ValuesSource } from "./client/types.js";
