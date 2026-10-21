import { i18n } from "@lingui/core";
import type { MutationOptions } from "@tanstack/react-query";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { ApiError } from "./errors";
import { auditEntriesQuery, environmentsQuery } from "./queries";
import { createQueryClient } from "./query-client";

const recentActivity = auditEntriesQuery({ project: "checkout" }, {}, 10).queryKey;
const environments = environmentsQuery("checkout").queryKey;

beforeEach(() => activateForTests("en"));

function seeded() {
  const queryClient = createQueryClient(i18n);
  queryClient.setQueryData(recentActivity, { items: [], next_cursor: null });
  queryClient.setQueryData(environments, []);
  return queryClient;
}

/** Runs one mutation to its end, as a screen would, refused or not. */
async function change(
  queryClient: ReturnType<typeof createQueryClient>,
  options: MutationOptions<unknown, unknown, void>,
) {
  const mutation = queryClient
    .getMutationCache()
    .build(queryClient, { ...options, meta: { toast: false, ...options.meta } });
  await mutation.execute(undefined).catch(() => undefined);
}

const succeeds = () => Promise.resolve();
const refused = () => Promise.reject(new ApiError(409, { code: "conflict" }));

describe("the console's query client", () => {
  it("reads every audit log again once a change succeeds, since the change is an entry", async () => {
    const queryClient = seeded();

    await change(queryClient, { mutationFn: succeeds });

    expect(queryClient.getQueryState(recentActivity)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(environments)?.isInvalidated).toBe(false);
  });

  it("leaves the audit logs as they are after a refused change", async () => {
    const queryClient = seeded();

    await change(queryClient, { mutationFn: refused });

    expect(queryClient.getQueryState(recentActivity)?.isInvalidated).toBe(false);
  });

  it("leaves the audit logs as they are after signing in or out, which change the session only", async () => {
    const queryClient = seeded();

    await change(queryClient, { mutationFn: succeeds, meta: { readsAuditAgain: false } });

    expect(queryClient.getQueryState(recentActivity)?.isInvalidated).toBe(false);
  });
});
