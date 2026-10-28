import type { Webhook, WebhookEventTypeInfo } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";

import { WebhookEditor } from "./webhook-editor";

const catalogue: WebhookEventTypeInfo[] = [
  { type: "flag.created", description: "A flag was created." },
  { type: "flag.kill_switch", description: "The kill switch was used." },
  { type: "segment.created", description: "A segment was created." },
  { type: "audit.entry", description: "Every audit entry." },
  { type: "ping", description: "A test." },
];

const environments = {
  items: [
    { id: "e1", key: "production", name: "Production" },
    { id: "e2", key: "staging", name: "Staging" },
  ],
};

const created: Webhook & { secret: string } = {
  id: "0192a000-0000-7000-8000-000000000001",
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
  secret: "s3cret",
};

const common = {
  "GET /webhook-event-types": { status: 200, body: { items: catalogue } },
  "GET /projects/checkout/environments": { status: 200, body: environments },
};

describe("the webhook editor", () => {
  it("offers the catalogue's types grouped, without the ping, and sends what was chosen", async () => {
    const server = fakeServer({
      ...common,
      "POST /projects/checkout/webhooks": { status: 201, body: created },
    });
    let saved: unknown;
    renderWithProviders(
      <WebhookEditor
        scope={{ project: "checkout" }}
        layout="dialog"
        onSaved={(webhook) => (saved = webhook)}
      />,
    );
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Name"), "Release bot");
    await user.type(screen.getByLabelText("URL"), "https://hooks.example.com/flagnetic");
    expect(await screen.findByRole("checkbox", { name: /flag\.created/ })).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: /^ping/ })).toBeNull();
    await user.click(screen.getByRole("checkbox", { name: /flag\.kill_switch/ }));
    await user.click(await screen.findByRole("checkbox", { name: "Staging" }));
    await user.click(screen.getByRole("button", { name: "Create webhook" }));

    await waitFor(() => expect(saved).toBeDefined());
    const sent = (await server.requests.find((r) => r.method === "POST")!.json()) as unknown;
    expect(sent).toEqual({
      name: "Release bot",
      url: "https://hooks.example.com/flagnetic",
      events: ["flag.kill_switch"],
      environments: ["staging"],
      enabled: true,
    });
  });

  it("covers every individual type with the every-event choice", async () => {
    const server = fakeServer({
      ...common,
      "POST /projects/checkout/webhooks": { status: 201, body: created },
    });
    renderWithProviders(
      <WebhookEditor scope={{ project: "checkout" }} layout="dialog" onSaved={() => undefined} />,
    );
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Name"), "Chat");
    await user.type(screen.getByLabelText("URL"), "https://hooks.example.com/chat");
    await user.click(await screen.findByRole("checkbox", { name: /^Every event/ }));
    expect(screen.getByRole("checkbox", { name: /flag\.created/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Create webhook" }));

    await waitFor(() => expect(server.requests.some((r) => r.method === "POST")).toBe(true));
    const sent = (await server.requests.find((r) => r.method === "POST")!.json()) as {
      events: string[];
    };
    expect(sent.events).toEqual(["*"]);
  });

  it("asks for a name, a URL and an event type before sending anything", async () => {
    const server = fakeServer(common);
    renderWithProviders(
      <WebhookEditor scope={{ project: "checkout" }} layout="dialog" onSaved={() => undefined} />,
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Create webhook" }));
    expect(await screen.findByText("Enter a name.")).toBeInTheDocument();
    expect(screen.getByText("Enter the URL the events are posted to.")).toBeInTheDocument();
    expect(screen.getByText("Choose at least one event type.")).toBeInTheDocument();
    expect(server.requests.some((request) => request.method === "POST")).toBe(false);
  });

  it("explains a target the instance refuses under the URL field", async () => {
    fakeServer({
      ...common,
      "POST /projects/checkout/webhooks": problem(422, "private_network_target"),
    });
    renderWithProviders(
      <WebhookEditor scope={{ project: "checkout" }} layout="dialog" onSaved={() => undefined} />,
    );
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Name"), "Internal");
    await user.type(screen.getByLabelText("URL"), "https://10.0.0.5/hook");
    await user.click(await screen.findByRole("checkbox", { name: /flag\.created/ }));
    await user.click(screen.getByRole("button", { name: "Create webhook" }));

    const url = screen.getByLabelText("URL");
    await waitFor(() =>
      expect(url).toHaveAccessibleDescription(
        expect.stringContaining("does not reach targets on its own network"),
      ),
    );
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("is an instance webhook of audit entries alone, with no types or environments to choose", async () => {
    const server = fakeServer({
      ...common,
      "POST /webhooks": { status: 201, body: { ...created, events: ["audit.entry"] } },
    });
    renderWithProviders(
      <WebhookEditor scope="instance" layout="dialog" onSaved={() => undefined} />,
    );
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Name"), "Archive");
    await user.type(screen.getByLabelText("URL"), "https://archive.example.com/in");
    expect(screen.queryByRole("checkbox", { name: /flag\.created/ })).toBeNull();
    expect(screen.queryByText("Environments")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Create webhook" }));

    await waitFor(() => expect(server.requests.some((r) => r.method === "POST")).toBe(true));
    const sent = (await server.requests.find((r) => r.method === "POST")!.json()) as {
      events: string[];
      environments: string[];
    };
    expect(sent.events).toEqual(["audit.entry"]);
    expect(sent.environments).toEqual([]);
  });

  it("saves an edit onto the version it was read at, sending only what changed", async () => {
    const server = fakeServer({
      ...common,
      "PATCH /projects/checkout/webhooks/0192a000-0000-7000-8000-000000000001": {
        status: 200,
        body: { ...created, name: "Deploy bot", version: 2 },
      },
    });
    const { secret: _secret, ...webhook } = created;
    const saved = vi.fn();
    renderWithProviders(
      <WebhookEditor
        scope={{ project: "checkout" }}
        webhook={webhook}
        layout="page"
        onSaved={saved}
      />,
    );
    const user = userEvent.setup();
    const name = await screen.findByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Deploy bot");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(server.requests.some((r) => r.method === "PATCH")).toBe(true));
    const request = server.requests.find((r) => r.method === "PATCH")!;
    expect(request.headers.get("If-Match")).toBe('"1"');
    expect(await request.json()).toEqual({ name: "Deploy bot" });
    await waitFor(() =>
      expect(saved).toHaveBeenCalledWith(
        expect.objectContaining({ name: "Deploy bot", version: 2 }),
      ),
    );
  });

  it("leaves enabling a subscription the platform disabled to the enable action", async () => {
    fakeServer(common);
    const { secret: _secret, ...webhook } = created;
    renderWithProviders(
      <WebhookEditor
        scope={{ project: "checkout" }}
        webhook={{ ...webhook, enabled: false, disabled_reason: "persistent_failure" }}
        layout="page"
        onSaved={() => undefined}
      />,
    );
    await screen.findByLabelText("Name");
    expect(
      within(document.body).queryByRole("switch", { name: "Enabled" }),
    ).not.toBeInTheDocument();
  });
});
