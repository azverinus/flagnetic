import type { Webhook } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { AccessProvider } from "@/components/access-provider";
import { fakeServer } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { ProjectWebhookBanner } from "./webhook-disabled-banner";

const base: Webhook = {
  id: "0192a000-0000-7000-8000-000000000001",
  name: "Release bot",
  url: "https://hooks.example.com/flagnetic",
  events: ["*"],
  environments: [],
  enabled: true,
  disabled_reason: null,
  created_by: null,
  last_success_at: null,
  consecutive_failures: 0,
  previous_secret_expires_at: null,
  created_at: "2026-10-25T10:00:00Z",
  updated_at: "2026-10-25T10:00:00Z",
  version: 1,
};
const platformDisabled: Webhook = {
  ...base,
  enabled: false,
  disabled_reason: "persistent_failure",
  consecutive_failures: 25,
};
const memberDisabled: Webhook = {
  ...base,
  id: "0192a000-0000-7000-8000-000000000002",
  name: "Chat",
  enabled: false,
};

const roles = {
  items: [
    { name: "maintainer", permissions: ["project.read", "webhook.manage"] },
    { name: "viewer", permissions: ["project.read"] },
  ],
};

function serve(permissions: string[], items: () => Webhook[]) {
  return fakeServer({
    "GET /roles": { status: 200, body: roles },
    "GET /projects/checkout/me": { status: 200, body: { grants: [], permissions } },
    "GET /projects/checkout/webhooks": () => ({ status: 200, body: { items: items() } }),
    [`POST /projects/checkout/webhooks/${base.id}/enable`]: () => {
      current = [{ ...base, version: 2 }, memberDisabled];
      return { status: 200, body: current[0] };
    },
  });
}

let current: Webhook[] = [];

async function renderBanner() {
  return renderWithRouter(() => (
    <AccessProvider project="checkout">
      <ProjectWebhookBanner project="checkout" />
    </AccessProvider>
  ));
}

describe("the disabled webhooks banner", () => {
  it("names a subscription the platform disabled, to those who manage webhooks", async () => {
    current = [platformDisabled, memberDisabled];
    serve(["project.read", "webhook.manage"], () => current);
    await renderBanner();

    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent("The platform disabled 1 webhook");
    expect(screen.getByRole("link", { name: "Release bot" })).toBeInTheDocument();
    // A subscription a member disabled is not the platform's news.
    expect(screen.getAllByRole("listitem")).toHaveLength(1);
  });

  it("goes once the subscription is enabled again", async () => {
    current = [platformDisabled];
    serve(["project.read", "webhook.manage"], () => current);
    await renderBanner();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Enable Release bot" }));
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  });

  it("says nothing when nothing was disabled by the platform", async () => {
    current = [base, memberDisabled];
    serve(["project.read", "webhook.manage"], () => current);
    await renderBanner();
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  });

  it("is not shown, nor the list read, to someone who cannot manage webhooks", async () => {
    current = [platformDisabled];
    const server = serve(["project.read"], () => current);
    await renderBanner();
    await waitFor(() =>
      expect(server.requests.some((request) => request.url.endsWith("/me"))).toBe(true),
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(server.requests.some((request) => request.url.endsWith("/webhooks"))).toBe(false);
  });
});
