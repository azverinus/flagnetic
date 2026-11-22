// The deprecation log: an operation of the SDK API the server has deprecated
// answers with a `Deprecation` header (and a `Sunset` header when a date is
// set). The client writes one warning per operation and says nothing more:
// the call itself is answered and read as if the header were not there.
import { SDK_API_PATH } from "../version.js";

/** Writes one deprecation warning; the fields name the operation and carry the headers' values. */
export type DeprecationReporter = (fields: Readonly<Record<string, unknown>>) => void;

/**
 * The operation a request URL addresses: the path after the SDK API's
 * prefix (`ruleset`, `stream`, `evaluate`, `events`), or the whole path for a
 * URL outside it.
 */
function operationOf(input: Parameters<typeof fetch>[0]): string {
  let path: string;
  try {
    path = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url).pathname;
  } catch {
    return "unknown";
  }
  const at = path.indexOf(`${SDK_API_PATH}/`);
  return at < 0 ? path : path.slice(at + SDK_API_PATH.length + 1);
}

/**
 * `fetch` that reports the first `Deprecation` header each operation answers
 * with. It reads headers only, never the body, and never throws: a failing
 * reporter is the caller's code, and the response goes on to the caller
 * whatever happens here.
 */
export function reportingDeprecations(inner: typeof fetch, report: DeprecationReporter): typeof fetch {
  const reported = new Set<string>();
  return async (input, init) => {
    const res = await inner(input, init);
    try {
      const deprecation = res.headers.get("Deprecation");
      if (deprecation !== null && deprecation.trim() !== "") {
        const operation = operationOf(input);
        if (!reported.has(operation)) {
          reported.add(operation);
          const sunset = res.headers.get("Sunset");
          report({ operation, deprecation, ...(sunset !== null && sunset.trim() !== "" && { sunset }) });
        }
      }
    } catch {
      // A header that cannot be read must not fail the request.
    }
    return res;
  };
}
