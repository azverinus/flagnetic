import type { QueryClient } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { AccessProvider } from "@/components/access-provider";
import { NeedsAttention } from "@/components/needs-attention";
import type { Permission } from "@/lib/access-names";
import { fakeServer, problem } from "@/test/fake-server";
import { clickhouseSink } from "@/test/sinks";
import { activateForTests, renderWithRouter } from "@/test/render";

beforeEach(() => activateForTests("en"));

const role = {
  name: "owner",
  permissions: ["project.read", "change_request.approve", "sink.manage", "webhook.manage"],
};

const webhook = {
  id: "0192a000-0000-7000-8000-0000000000d1",
  name: "Pager",
  url: "https://receiver.example/hook",
  events: ["*"],
  environments: [],
  enabled: false,
  disabled_reason: "persistent_failure",
  created_by: null,
  last_success_at: null,
  consecutive_failures: 12,
  previous_secret_expires_at: null,
  created_at: "2026-11-01T10:00:00Z",
  updated_at: "2026-11-01T10:00:00Z",
  version: 1,
};

const changeRequest = (id: string) => ({ id });

/** A caller holding these permissions, with answers for every list the card reads. */
function server(permissions: Permission[], answers: Record<string, unknown> = {}) {
  return fakeServer({
    "GET /roles": { status: 200, body: { items: [role] } },
    "GET /projects/checkout/me": { status: 200, body: { grants: [], permissions } },
    "GET /projects/checkout/change-requests": {
      status: 200,
      body: { items: [changeRequest("a"), changeRequest("b")], next_cursor: null },
    },
    "GET /projects/checkout/stale-flags": {
      status: 200,
      body: { items: [{ key: "old" }], next_cursor: null },
    },
    "GET /projects/checkout/sinks": {
      status: 200,
      body: { items: [{ ...clickhouseSink, at_risk: true }] },
    },
    "GET /projects/checkout/webhooks": { status: 200, body: { items: [webhook] } },
    ...(answers as Record<string, never>),
  });
}

const all: Permission[] = [
  "project.read",
  "change_request.approve",
  "sink.manage",
  "webhook.manage",
];

const render = () =>
  renderWithRouter(() => (
    <AccessProvider project="checkout">
      <NeedsAttention project="checkout" />
    </AccessProvider>
  ));

describe("the overview's needs attention card", () => {
  it("lists a row for each thing waiting, linking to its screen", async () => {
    server(all);
    await render();

    const list = await screen.findByRole("list", { name: "Needs attention" });
    expect(
      await screen.findByRole("link", { name: /2 change requests await your decision/ }),
    ).toHaveAttribute("href", "/projects/checkout/change-requests?tab=awaiting");
    expect(screen.getByRole("link", { name: /1 stale flag/ })).toHaveAttribute(
      "href",
      "/projects/checkout/stale-flags",
    );
    expect(screen.getByRole("link", { name: /1 sink needs attention/ })).toHaveAttribute(
      "href",
      "/projects/checkout/sinks",
    );
    expect(screen.getByRole("link", { name: /1 webhook disabled/ })).toHaveAttribute(
      "href",
      "/projects/checkout/webhooks",
    );
    expect(list.querySelectorAll("li")).toHaveLength(4);
  });

  it("omits the rows of what the member may not read, and never asks for them", async () => {
    const { requests } = server(["project.read"]);
    await render();

    expect(await screen.findByRole("link", { name: /1 stale flag/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /change request/ })).toBeNull();
    expect(screen.queryByRole("link", { name: /sink/ })).toBeNull();
    expect(screen.queryByRole("link", { name: /webhook/ })).toBeNull();
    const paths = requests.map((request) => new URL(request.url).pathname);
    expect(paths).not.toContain("/api/v1/projects/checkout/sinks");
    expect(paths).not.toContain("/api/v1/projects/checkout/webhooks");
    expect(paths).not.toContain("/api/v1/projects/checkout/change-requests");
  });

  it("omits a row with nothing to say, and the card when no row is left", async () => {
    server(all, {
      "GET /projects/checkout/change-requests": {
        status: 200,
        body: { items: [], next_cursor: null },
      },
      "GET /projects/checkout/stale-flags": { status: 200, body: { items: [], next_cursor: null } },
      "GET /projects/checkout/sinks": { status: 200, body: { items: [clickhouseSink] } },
      "GET /projects/checkout/webhooks": {
        status: 200,
        body: { items: [{ ...webhook, enabled: true, disabled_reason: null }] },
      },
    });
    const { queryClient } = await render();

    await untilIdle(queryClient);
    expect(screen.queryByRole("list", { name: "Needs attention" })).toBeNull();
    expect(screen.queryByText("Needs attention")).toBeNull();
  });

  it("says there are more when the stale flags run past a page", async () => {
    server(["project.read"], {
      "GET /projects/checkout/stale-flags": {
        status: 200,
        body: { items: [{ key: "a" }, { key: "b" }], next_cursor: "next" },
      },
    });
    await render();

    expect(await screen.findByRole("link", { name: /2\+ stale flags/ })).toBeInTheDocument();
  });

  it("says there are more when the change requests awaiting a decision run past the count", async () => {
    server(all, {
      "GET /projects/checkout/change-requests": {
        status: 200,
        body: {
          items: Array.from({ length: 100 }, (_, index) => changeRequest(String(index))),
          next_cursor: "next",
        },
      },
    });
    await render();

    expect(
      await screen.findByRole("link", { name: /More than 99 change requests await your decision/ }),
    ).toHaveAttribute("href", "/projects/checkout/change-requests?tab=awaiting");
  });

  it("leaves out the row of a list that failed to read", async () => {
    server(all, { "GET /projects/checkout/sinks": problem(500, "internal") });
    await render();

    expect(await screen.findByRole("link", { name: /1 webhook disabled/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /sink/ })).toBeNull();
  });
});

/** Waits until every query of the client has settled. */
async function untilIdle(queryClient: QueryClient) {
  await waitFor(() => {
    expect(queryClient.isFetching()).toBe(0);
    expect(queryClient.getQueryCache().getAll().length).toBeGreaterThan(0);
  });
}
