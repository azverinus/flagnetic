import type { Environment } from "@flagnetic/api";
import { getEnvironment } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import { fakeServer, reply, takeUndeclared } from "./fake-server";

const staging: Environment = {
  id: "e1",
  key: "staging",
  name: "Staging",
  sort_order: 0,
  version: 1,
  protected: false,
};

describe("the fake server", () => {
  it("answers a declared route with the body it was given", async () => {
    fakeServer({ "GET /projects/shop/environments/staging": reply<Environment>(staging) });

    const { data } = await getEnvironment({ path: { project: "shop", environment: "staging" } });

    expect(data).toEqual(staging);
    expect(takeUndeclared()).toEqual([]);
  });

  it("records a request to a route that was not declared, which fails the test that made it", async () => {
    fakeServer({ "GET /projects/shop/environments/staging": reply<Environment>(staging) });

    await getEnvironment({ path: { project: "shop", environment: "stagging" } });

    expect(takeUndeclared()).toEqual(["GET /projects/shop/environments/stagging"]);
  });

  it("keeps what an earlier server in the same test was asked undeclared", async () => {
    fakeServer({});
    await getEnvironment({ path: { project: "shop", environment: "staging" } });
    fakeServer({ "GET /projects/shop/environments/staging": reply<Environment>(staging) });

    expect(takeUndeclared()).toEqual(["GET /projects/shop/environments/staging"]);
  });

  it("checks a body against the generated type, so a renamed field fails tsc", () => {
    // @ts-expect-error `protected_at` is not a field of an environment
    reply<Environment>({ ...staging, protected_at: "2026-11-01T00:00:00Z" });
    // @ts-expect-error `name` is required
    reply<Environment>({ id: "e1", key: "staging" });
    // @ts-expect-error a route is a method and a path under /api/v1
    fakeServer({ "/projects": reply(null) });
    expect(true).toBe(true);
  });
});
