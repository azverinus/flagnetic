import type { Webhook } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { WebhookScreen } from "./webhook-detail";

const id = "0192a000-0000-7000-8000-000000000001";
const webhook: Webhook = {
  id,
  name: "Release bot",
  url: "https://hooks.example.com/flagnetic",
  events: ["flag.kill_switch"],
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

const base = `/projects/checkout/webhooks/${id}`;
const common = {
  "GET /webhook-event-types": { status: 200, body: { items: [] } },
  "GET /projects/checkout/environments": { status: 200, body: { items: [] } },
};

function renderScreen(onSearch = vi.fn()) {
  return renderWithRouter(() => (
    <WebhookScreen scope={{ project: "checkout" }} webhook={id} search={{}} onSearch={onSearch} />
  )).then((rendered) => ({ ...rendered, onSearch }));
}

describe("a webhook's screen", () => {
  it("shows the subscription and its settings", async () => {
    fakeServer({ ...common, [`GET ${base}`]: { status: 200, body: webhook } });
    await renderScreen();

    expect(await screen.findByRole("heading", { name: "Release bot", level: 1 })).toBeVisible();
    expect(screen.getByLabelText("URL")).toHaveValue(webhook.url);
    expect(screen.getByRole("tab", { name: "Settings", selected: true })).toBeVisible();
  });

  it("says the subscription is missing when the server has none", async () => {
    fakeServer({ ...common });
    await renderScreen();
    expect(await screen.findByRole("heading", { name: "Webhook not found" })).toBeVisible();
  });

  it("sends a test and moves to the delivery log", async () => {
    const server = fakeServer({
      ...common,
      [`GET ${base}`]: { status: 200, body: webhook },
      [`POST ${base}/test`]: {
        status: 202,
        body: {
          id: "0192a000-0000-7000-8000-0000000000a1",
          event_id: "0192a000-0000-7000-8000-0000000000e1",
          event_type: "ping",
          status: "pending",
          attempts: [],
          next_attempt_at: "2026-10-26T09:00:05Z",
          created_at: "2026-10-26T09:00:00Z",
        },
      },
    });
    const { onSearch } = await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Send test" }));
    await waitFor(() => expect(onSearch).toHaveBeenCalledWith({ tab: "deliveries" }));
    expect(server.requests.some((r) => r.url.endsWith("/test"))).toBe(true);
  });

  it("rotates the secret after confirming and shows the new one once", async () => {
    const secret = "n3w-s3cret-zz99";
    fakeServer({
      ...common,
      [`GET ${base}`]: { status: 200, body: webhook },
      [`POST ${base}/rotate-secret`]: {
        status: 200,
        body: {
          ...webhook,
          version: 2,
          previous_secret_expires_at: "2026-10-27T09:00:00Z",
          secret,
        },
      },
    });
    const { queryClient } = await renderScreen();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Rotate secret" }));
    const confirm = await screen.findByRole("dialog", { name: "Rotate the secret of Release bot" });
    await user.click(within(confirm).getByRole("button", { name: "Rotate secret" }));

    const reveal = await screen.findByRole("dialog", { name: "The webhook's signing secret" });
    expect(within(reveal).getByText(secret)).toBeInTheDocument();
    expect(reveal).toHaveTextContent("keeps signing beside this one for a day");
    await user.click(within(reveal).getByLabelText("I have stored it somewhere safe"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());
    // The value lives only where the screen showed it: not in the mutation
    // cache, and not in the subscription the screen keeps.
    await waitFor(() =>
      expect(
        JSON.stringify(
          queryClient
            .getMutationCache()
            .getAll()
            .map((m) => m.state),
        ),
      ).not.toContain(secret),
    );
    expect(
      JSON.stringify(
        queryClient
          .getQueryCache()
          .getAll()
          .map((query) => query.state.data),
      ),
    ).not.toContain(secret);
  });

  it("offers to enable a subscription the platform disabled, and cannot send it a test", async () => {
    fakeServer({
      ...common,
      [`GET ${base}`]: {
        status: 200,
        body: { ...webhook, enabled: false, disabled_reason: "persistent_failure" },
      },
    });
    await renderScreen();

    expect(await screen.findByRole("button", { name: "Enable" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Send test" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("The platform disabled this webhook");
  });
});
