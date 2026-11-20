import { client } from "@flagnetic/api";

/** One answer of the fake management API. */
export type FakeAnswer = { status: number; body?: unknown; headers?: Record<string, string> };

/** The methods the console calls the management API with. */
type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

/** A route of the fake server: a method and a path under /api/v1, such as `GET /projects/shop/flags`. */
export type Route = `${Method} /${string}`;

type Handler = (request: Request) => FakeAnswer | Promise<FakeAnswer>;

/**
 * An answer whose body is checked against the type the generated client
 * names for it, so a field renamed in the API contract fails `tsc` in the
 * tests that still write the old one: `reply<Flag>({ ... })`. A bare object
 * in a route is only as checked as the test's own cast.
 */
export function reply<Body>(
  body: Body,
  status = 200,
  headers?: Record<string, string>,
): FakeAnswer {
  return { status, body, headers };
}

/** A problem document as the server writes one. */
export function problem(
  status: number,
  code: string,
  extra: Record<string, unknown> = {},
): FakeAnswer {
  return {
    status,
    headers: { "Content-Type": "application/problem+json" },
    body: { type: "about:blank", title: code, status, code, request_id: "req-1", ...extra },
  };
}

let undeclared: string[] = [];

/**
 * The requests made to a route no test declared since the last call. A
 * route key with a typo, or a request the screen began making, would
 * otherwise be answered "not found" and shown as a refusal the test never
 * looked at; the test setup fails the test that made one.
 */
export function takeUndeclared(): string[] {
  const taken = undeclared;
  undeclared = [];
  return taken;
}

/**
 * Points the generated client at handlers keyed by "METHOD /path" (the path
 * under /api/v1), and records every request made. A request to a route not
 * declared fails the test; a test that means to see a refusal declares it
 * with `problem`.
 */
export function fakeServer(routes: Partial<Record<Route, Handler | FakeAnswer>>) {
  undeclared = [];
  const requests: Request[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    requests.push(request.clone());
    const path = new URL(request.url).pathname.replace(/^\/api\/v1/, "");
    const key = `${request.method} ${path}` as Route;
    const route = routes[key];
    if (route === undefined) undeclared.push(key);
    const answer =
      typeof route === "function" ? await route(request) : (route ?? problem(404, "not_found"));
    return new Response(answer.body === undefined ? null : JSON.stringify(answer.body), {
      status: answer.status,
      headers: { "Content-Type": "application/json", ...answer.headers },
    });
  };
  client.setConfig({ ...client.getConfig(), baseUrl: "http://console.test/api/v1", fetch });
  return { requests };
}
