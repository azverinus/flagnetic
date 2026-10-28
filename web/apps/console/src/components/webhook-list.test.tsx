import type { Webhook } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";

import { WebhookList } from "./webhook-list";

const webhook: Webhook = {
  id: "0192a000-0000-7000-8000-000000000001",
  name: "Release bot",
  url: "https://hooks.example.com/flagnetic",
  events: ["flag.kill_switch", "flag.created"],
  environments: [],
  enabled: true,
  disabled_reason: null,
  created_by: null,
  last_success_at: "2026-10-26T09:00:00Z",
  consecutive_failures: 0,
  previous_secret_expires_at: null,
  created_at: "2026-10-25T10:00:00Z",
  updated_at: "2026-10-25T10:00:00Z",
  version: 1,
};
const disabled: Webhook = {
  ...webhook,
  id: "0192a000-0000-7000-8000-000000000002",
  name: "Chat",
  enabled: false,
  disabled_reason: "persistent_failure",
  consecutive_failures: 25,
  last_success_at: null,
};

const list = "/projects/checkout/webhooks";
const catalogue = {
  items: [
    { type: "flag.kill_switch", description: "The kill switch was used." },
    { type: "flag.created", description: "A flag was created." },
  ],
};

function renderList() {
  return renderWithRouter(() => <WebhookList scope={{ project: "checkout" }} />);
}

describe("the project's webhooks", () => {
  it("list each subscription with what it is sent and whether it is enabled", async () => {
    fakeServer({ [`GET ${list}`]: { status: 200, body: { items: [webhook, disabled] } } });
    await renderList();

    const bot = await screen.findByRole("row", { name: /Release bot/ });
    expect(bot).toHaveTextContent("Enabled");
    expect(bot).toHaveTextContent("2 event types");
    expect(bot).toHaveTextContent("https://hooks.example.com/flagnetic");
    const chat = screen.getByRole("row", { name: /Chat/ });
    expect(chat).toHaveTextContent("Disabled after persistent failure");
    expect(chat).toHaveTextContent("25");
    expect(within(chat).getByRole("button", { name: "Re-enable Chat" })).toBeInTheDocument();
    expect(within(bot).queryByRole("button", { name: /enable/i })).toBeNull();
  });

  it("offer the first webhook when there is none", async () => {
    fakeServer({ [`GET ${list}`]: { status: 200, body: { items: [] } } });
    await renderList();
    expect(await screen.findByText("No webhook yet.")).toBeInTheDocument();
  });

  it("enable a disabled subscription in one click", async () => {
    let items = [disabled];
    const server = fakeServer({
      [`GET ${list}`]: () => ({ status: 200, body: { items } }),
      [`POST ${list}/${disabled.id}/enable`]: () => {
        items = [{ ...disabled, enabled: true, disabled_reason: null, consecutive_failures: 0 }];
        return { status: 200, body: items[0] };
      },
    });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Re-enable Chat" }));
    await waitFor(() =>
      expect(screen.getByRole("row", { name: /Chat/ })).toHaveTextContent("Enabled"),
    );
    expect(server.requests.some((request) => request.method === "POST")).toBe(true);
  });

  it("create a subscription and show its secret once, kept out of the mutation cache", async () => {
    const secret = "k3y-s3cret-ab12";
    let items: Webhook[] = [];
    fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      "GET /projects/checkout/environments": { status: 200, body: { items: [] } },
      [`GET ${list}`]: () => ({ status: 200, body: { items } }),
      [`POST ${list}`]: () => {
        items = [webhook];
        return { status: 201, body: { ...webhook, secret } };
      },
    });
    const { queryClient } = await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "New webhook" }));
    const dialog = await screen.findByRole("dialog", { name: "New webhook" });
    await user.type(within(dialog).getByLabelText("Name"), "Release bot");
    await user.type(within(dialog).getByLabelText("URL"), "https://hooks.example.com/flagnetic");
    await user.click(await within(dialog).findByRole("checkbox", { name: /flag\.kill_switch/ }));
    await user.click(within(dialog).getByRole("button", { name: "Create webhook" }));

    const reveal = await screen.findByRole("dialog", { name: "The webhook's signing secret" });
    expect(within(reveal).getByText(secret)).toBeInTheDocument();
    expect(within(reveal).getByRole("button", { name: "Close" })).toBeDisabled();
    await user.click(within(reveal).getByLabelText("I have stored it somewhere safe"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());
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
  });

  it("confirm before deleting, and explain a refusal in the dialog", async () => {
    fakeServer({
      [`GET ${list}`]: { status: 200, body: { items: [webhook] } },
      [`DELETE ${list}/${webhook.id}`]: problem(403, "forbidden", { permission: "webhook.manage" }),
    });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Delete Release bot" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete Release bot" });
    await user.click(within(dialog).getByRole("button", { name: "Delete webhook" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "You need the permission to manage webhook subscriptions.",
    );
  });
});

describe("the instance's webhooks", () => {
  it("are listed under their own title from the instance's endpoint", async () => {
    fakeServer({ "GET /webhooks": { status: 200, body: { items: [webhook] } } });
    await renderWithRouter(() => <WebhookList scope="instance" />);
    expect(await screen.findByRole("heading", { name: "Instance webhooks" })).toBeInTheDocument();
    expect(await screen.findByRole("row", { name: /Release bot/ })).toBeInTheDocument();
  });
});
