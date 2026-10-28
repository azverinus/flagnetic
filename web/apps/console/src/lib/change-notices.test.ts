import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";

import { applyNotice, applyRefetch, noticeKeys, parseNotice } from "./change-notices";

describe("parseNotice", () => {
  it("reads a notice, with no environment or version when there are none", () => {
    expect(
      parseNotice(
        "e1",
        JSON.stringify({
          kind: "flag",
          key: "checkout",
          environment: "prod",
          version: 4,
          actor: {},
        }),
      ),
    ).toEqual({ id: "e1", kind: "flag", key: "checkout", environment: "prod", version: 4 });
    expect(
      parseNotice("e2", '{"kind":"member","key":"a@b","environment":null,"version":null}'),
    ).toEqual({
      id: "e2",
      kind: "member",
      key: "a@b",
      environment: null,
      version: null,
    });
  });

  it("ignores data that is not a notice", () => {
    expect(parseNotice("e", "not json")).toBeUndefined();
    expect(parseNotice("e", "{}")).toBeUndefined();
    expect(parseNotice("e", "[]")).toBeUndefined();
  });
});

describe("noticeKeys", () => {
  it.each([
    ["flag", "flags"],
    ["experiment", "experiments"],
    ["segment", "segments"],
    ["environment", "environments"],
    ["attribute", "attributes"],
    ["unit_type", "unit-types"],
    ["member", "members"],
    ["grant", "members"],
    ["api_token", "tokens"],
    ["change_request", "change-requests"],
    ["webhook", "webhooks"],
  ])("a %s notice refreshes %s", (kind, segment) => {
    expect(noticeKeys("shop", { kind })).toContainEqual(["projects", "shop", segment]);
  });

  it("refreshes the whole project for a kind it does not know", () => {
    expect(noticeKeys("shop", { kind: "something_new" })).toEqual([["projects", "shop"]]);
  });
});

describe("applyNotice", () => {
  function seeded() {
    const queryClient = new QueryClient();
    const keys = {
      flags: ["projects", "shop", "flags", "list", {}],
      segments: ["projects", "shop", "segments", "list"],
      other: ["projects", "other", "flags", "list", {}],
      audit: ["audit", { project: "shop" }, {}, undefined],
      otherAudit: ["audit", { project: "other" }, {}, undefined],
      instanceAudit: ["audit", "instance", {}, undefined],
    };
    for (const key of Object.values(keys)) queryClient.setQueryData(key, []);
    return { queryClient, keys };
  }
  const stale = (queryClient: QueryClient, key: readonly unknown[]) =>
    queryClient.getQueryState(key)?.isInvalidated;

  it("refreshes the entity's queries and the project's audit log, and nothing of another project", () => {
    const { queryClient, keys } = seeded();
    applyNotice(queryClient, "shop", {
      id: "e",
      kind: "flag",
      key: "checkout",
      environment: null,
      version: 2,
    });
    expect(stale(queryClient, keys.flags)).toBe(true);
    expect(stale(queryClient, keys.audit)).toBe(true);
    expect(stale(queryClient, keys.other)).toBe(false);
    expect(stale(queryClient, keys.otherAudit)).toBe(false);
    expect(stale(queryClient, keys.instanceAudit)).toBe(false);
  });

  it("refreshes the segments for a flag notice: they count the flags whose rules name them", () => {
    const { queryClient, keys } = seeded();
    applyNotice(queryClient, "shop", {
      id: "e",
      kind: "flag",
      key: "checkout",
      environment: null,
      version: 2,
    });
    expect(stale(queryClient, keys.segments)).toBe(true);
  });

  it("refreshes the project list for a project notice, and nothing of another project", () => {
    const { queryClient, keys } = seeded();
    queryClient.setQueryData(["projects"], []);
    applyNotice(queryClient, "shop", {
      id: "e",
      kind: "project",
      key: "shop",
      environment: null,
      version: 3,
    });
    expect(stale(queryClient, ["projects"])).toBe(true);
    expect(stale(queryClient, keys.flags)).toBe(true);
    expect(stale(queryClient, keys.other)).toBe(false);
  });

  it("refreshes everything of the project on a refetch", () => {
    const { queryClient, keys } = seeded();
    applyRefetch(queryClient, "shop");
    expect(stale(queryClient, keys.flags)).toBe(true);
    expect(stale(queryClient, keys.segments)).toBe(true);
    expect(stale(queryClient, keys.audit)).toBe(true);
    expect(stale(queryClient, keys.other)).toBe(false);
  });
});
