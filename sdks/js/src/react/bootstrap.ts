// The script that hands the values document a page was rendered from to the
// browser, where the provider reads it back as its bootstrap.
import { createElement, type ReactElement } from "react";
import { serializeBootstrap } from "../internal/bootstrap.js";

export interface FlagneticBootstrapProps {
  /**
   * The values document the page was rendered from, the one passed to the
   * provider. `undefined`, what `values()` gives at stage `built_in`,
   * renders nothing.
   */
  readonly bootstrap: object | undefined;
  /** The property of `window` the document is assigned to: `__FLAGNETIC__` by default. */
  readonly variable?: string;
  /** The nonce a Content Security Policy that allows inline scripts by nonce asks for. */
  readonly nonce?: string;
}

// An identifier name, so `window.<variable>` is one property and nothing
// else; __proto__ is one too, but assigning it replaces window's prototype.
const identifier = /^[A-Za-z_$][\w$]*$/;

/**
 * Renders `<script>window.__FLAGNETIC__ = …;</script>` with the document
 * serialized by {@link serializeBootstrap}, so no flag value can end the
 * script early. Render it on the server with the document the provider
 * rendered from, outside the root the browser hydrates (the page's head),
 * and pass `window.__FLAGNETIC__` to the provider in the browser.
 *
 * ```tsx
 * const head = renderToStaticMarkup(<FlagneticBootstrap bootstrap={doc} />);
 * ```
 *
 * Throws a TypeError when `variable` is not a JavaScript identifier.
 */
export function FlagneticBootstrap(props: FlagneticBootstrapProps): ReactElement | null {
  const { bootstrap, variable = "__FLAGNETIC__", nonce } = props;
  if (!identifier.test(variable) || variable === "__proto__") {
    throw new TypeError(`flagnetic: ${JSON.stringify(variable)} is not a JavaScript identifier`);
  }
  if (bootstrap === undefined) return null;
  return createElement("script", {
    ...(nonce !== undefined && { nonce }),
    dangerouslySetInnerHTML: { __html: `window.${variable} = ${serializeBootstrap(bootstrap)};` },
  });
}
