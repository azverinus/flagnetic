import { client } from "@flagnetic/api";

/** One answer of the fake management API. */
export type FakeAnswer = { status: number; body?: unknown; headers?: Record<string, string> };

type Handler = (request: Request) => FakeAnswer | Promise<FakeAnswer>;

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

/**
 * Points the generated client at handlers keyed by "METHOD /path" (the path
 * under /api/v1), and records every request made.
 */
export function fakeServer(routes: Record<string, Handler | FakeAnswer>) {
  const requests: Request[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    requests.push(request.clone());
    const path = new URL(request.url).pathname.replace(/^\/api\/v1/, "");
    const route = routes[`${request.method} ${path}`];
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
