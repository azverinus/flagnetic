// The provider. It takes a client the application already owns, or the
// arguments to build one, and puts a source in React's context. It renders
// its children and nothing else: a flag change re-renders the components
// that read that flag, never the provider and never the tree under it.
import { createContext, createElement, useEffect, useState, type ReactElement, type ReactNode } from "react";
import { createClient, type FlagneticClient } from "../client/client.js";
import type { ClientOptions, ValuesSource } from "../client/types.js";
import { FlagneticSource } from "./source.js";

/** @internal The source the hooks read; a tree without a provider has none. */
export const SourceContext = createContext<FlagneticSource | undefined>(undefined);

export interface FlagneticProviderProps {
  /**
   * A client the application owns. It is used as it is and is never closed
   * by the provider. Leave it out and pass `sdkKey` instead to let the
   * provider own one.
   */
  readonly client?: FlagneticClient;
  /** The client key, when the provider builds and owns the client. */
  readonly sdkKey?: string;
  /** The platform's base URL, when the provider builds and owns the client. */
  readonly baseUrl?: string;
  /** The options the provider builds its client with, `context` above all. */
  readonly options?: ClientOptions;
  /**
   * The values document the page was rendered from. The hooks serve it while
   * the page is rendered and hydrated, and the client the provider builds
   * starts from it, so nothing changes under the user at hydration.
   * `undefined` — which is what `values()` gives at stage `built_in` — means
   * both sides render built-in defaults until the client has values.
   */
  readonly bootstrap?: ValuesSource | undefined;
  readonly children?: ReactNode;
}

/**
 * Puts a client, or the bootstrap document a page was rendered from, within
 * reach of {@link useFlag} and the other hooks.
 *
 * On a server, and while the page hydrates, the hooks serve the bootstrap
 * document: the first render in the browser is the one the server sent. The
 * provider builds its client after hydration, from the same document, so the
 * first refetch is the client's own and nothing changes under the user in
 * between.
 *
 * ```tsx
 * <FlagneticProvider sdkKey="fn_cli_…" baseUrl="https://flags.example.com"
 *                    options={{ context: { units: { user: id } } }}
 *                    bootstrap={window.__FLAGNETIC__}>
 *   <App />
 * </FlagneticProvider>
 * ```
 *
 * `sdkKey`, `baseUrl`, `options` and `bootstrap` are read when the provider
 * mounts, so an object literal in JSX is safe; give the provider a `key` to
 * build a client for another environment. A client passed in is followed
 * whenever it changes.
 */
export function FlagneticProvider(props: FlagneticProviderProps): ReactElement {
  const { client, children } = props;

  // Read once, at mount: an object literal in JSX is a new object on every
  // render and must not rebuild the client under the tree.
  const [built] = useState(() => {
    const { sdkKey, baseUrl, options, bootstrap } = props;
    const source = new FlagneticSource(bootstrap ?? options?.bootstrap, client, options?.logger);
    const owned =
      sdkKey === undefined
        ? undefined
        : () =>
            createClient(sdkKey, baseUrl ?? "", {
              ...options,
              ...(bootstrap !== undefined && { bootstrap }),
            });
    return { source, owned };
  });
  const { source, owned } = built;

  // A client the application owns is read from the first render on, so it
  // serves the server's render too; the one the provider owns is built after
  // hydration, and its first refetch happens then.
  useEffect(() => {
    if (client) return source.attach(client);
    if (!owned) return undefined;
    const ours = owned();
    const release = source.attach(ours);
    return () => {
      release();
      void ours.close();
    };
  }, [source, owned, client]);

  return createElement(SourceContext.Provider, { value: source }, children);
}
