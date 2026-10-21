import { describe, expect, it, vi } from "vitest";

import { ApiError, errorOf, unwrap } from "./errors";
import { ifMatch, managementFetch } from "./http";

function answer(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/problem+json" },
  });
}

describe("the fetch the generated client runs on", () => {
  it("reports an unauthenticated answer, wherever it happens", async () => {
    const onUnauthenticated = vi.fn();
    const send = managementFetch({
      fetch: async () => answer(401, { code: "session_expired" }),
      onUnauthenticated,
    });

    await send("http://console.test/api/v1/projects");

    expect(onUnauthenticated).toHaveBeenCalledOnce();
  });

  it("does not report a refused sign-in as an ended session", async () => {
    const onUnauthenticated = vi.fn();
    const send = managementFetch({
      fetch: async () => answer(401, { code: "invalid_credentials" }),
      onUnauthenticated,
    });

    await send("http://console.test/api/v1/auth/sign-in", { method: "POST" });

    expect(onUnauthenticated).not.toHaveBeenCalled();
  });

  it("sends a refused request once, adding no header of its own", async () => {
    // A request from another origin is refused outright; there is no token
    // to refresh and nothing a retry would change.
    const seen: Request[] = [];
    const send = managementFetch({
      fetch: async (input) => {
        seen.push(input as Request);
        return answer(403, { code: "cross_origin" });
      },
      onUnauthenticated: vi.fn(),
    });

    const response = await send("http://console.test/api/v1/projects", {
      method: "POST",
      body: "{}",
      headers: { "Content-Type": "application/json" },
    });

    expect(response.status).toBe(403);
    expect(seen).toHaveLength(1);
    expect([...seen[0]!.headers.keys()]).toEqual(["content-type"]);
  });

  it("leaves the refusal for the caller to read", async () => {
    const send = managementFetch({
      fetch: async () => answer(401, { code: "unauthenticated", detail: "sign in" }),
      onUnauthenticated: vi.fn(),
    });

    const response = await send("http://console.test/api/v1/me");

    expect(await response.json()).toMatchObject({ code: "unauthenticated", detail: "sign in" });
  });
});

describe("a refusal", () => {
  it("reads the code, the detail, the request ID and the wait the server sent", async () => {
    const error = await errorOf(
      answer(429, { code: "locked", detail: "locked", request_id: "req-9", retry_after: 42 }),
    );

    expect(error).toBeInstanceOf(ApiError);
    expect(error.status).toBe(429);
    expect(error.code).toBe("locked");
    expect(error.detail).toBe("locked");
    expect(error.requestId).toBe("req-9");
    expect(error.retryAfter).toBe(42);
  });

  it("falls back to the status when the body is not a problem document", async () => {
    const error = await errorOf(new Response("<html>gateway</html>", { status: 502 }));

    expect(error.code).toBe("internal");
    expect(error.requestId).toBe("");
  });

  it("is thrown from a generated operation's result, and a request that never arrived is unreachable", async () => {
    await expect(
      unwrap(
        Promise.resolve({
          error: { code: "not_found", request_id: "req-2" },
          response: answer(404),
        }),
      ),
    ).rejects.toMatchObject({ status: 404, code: "not_found", requestId: "req-2" });
    await expect(
      unwrap(Promise.resolve({ error: new TypeError("offline") })),
    ).rejects.toMatchObject({ status: 0, code: "unreachable" });
    await expect(
      unwrap(
        Promise.resolve({ data: { ok: true }, response: new Response(null, { status: 200 }) }),
      ),
    ).resolves.toEqual({ ok: true });
  });
});

describe("the version a write names", () => {
  it("is sent as the strong entity tag the server answered", () => {
    expect(ifMatch(7)).toEqual({ "If-Match": '"7"' });
  });
});
