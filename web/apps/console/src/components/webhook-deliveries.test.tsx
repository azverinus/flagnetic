import type { WebhookDelivery } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { renderWithProviders } from "@/test/render";
import type { WebhookSearch } from "@/lib/webhooks";

import { WebhookDeliveries } from "./webhook-deliveries";

const webhook = "0192a000-0000-7000-8000-000000000001";
const log = `/projects/checkout/webhooks/${webhook}/deliveries`;

const succeeded: WebhookDelivery = {
  id: "0192a000-0000-7000-8000-0000000000a1",
  event_id: "0192a000-0000-7000-8000-0000000000e1",
  event_type: "flag.kill_switch",
  status: "succeeded",
  attempts: [
    {
      n: 1,
      at: "2026-10-26T09:00:01Z",
      status_code: 200,
      latency_ms: 42,
      response_snippet: "received",
      error: null,
    },
  ],
  next_attempt_at: null,
  created_at: "2026-10-26T09:00:00Z",
};
const retrying: WebhookDelivery = {
  id: "0192a000-0000-7000-8000-0000000000a2",
  event_id: "0192a000-0000-7000-8000-0000000000e2",
  event_type: "ping",
  status: "pending",
  attempts: [
    {
      n: 1,
      at: "2026-10-26T09:05:01Z",
      status_code: null,
      latency_ms: 3,
      response_snippet: null,
      error: "connection refused",
    },
  ],
  next_attempt_at: "2026-10-26T09:05:11Z",
  created_at: "2026-10-26T09:05:00Z",
};

const catalogue = { items: [{ type: "flag.kill_switch", description: "The kill switch." }] };

function renderLog(search: WebhookSearch = { tab: "deliveries" }) {
  const onSearch = vi.fn();
  renderWithProviders(
    <WebhookDeliveries
      scope={{ project: "checkout" }}
      webhook={webhook}
      search={search}
      onSearch={onSearch}
    />,
  );
  return onSearch;
}

describe("a webhook's delivery log", () => {
  it("lists each delivery with where it stands and what the receiver answered", async () => {
    fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      [`GET ${log}`]: { status: 200, body: { items: [retrying, succeeded], next_cursor: null } },
    });
    renderLog();

    const ping = await screen.findByRole("row", { name: /ping/ });
    expect(ping).toHaveTextContent("Pending");
    expect(ping).toHaveTextContent("No response");
    const kill = screen.getByRole("row", { name: /flag\.kill_switch/ });
    expect(kill).toHaveTextContent("Succeeded");
    expect(kill).toHaveTextContent("200");
  });

  it("asks the server for the filters in the address", async () => {
    const server = fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      [`GET ${log}`]: { status: 200, body: { items: [], next_cursor: null } },
    });
    renderLog({ tab: "deliveries", status: "failed", event_type: "ping" });

    expect(await screen.findByText("No delivery matches the filters.")).toBeInTheDocument();
    const asked = new URL(server.requests.find((r) => r.url.includes("/deliveries"))!.url);
    expect(asked.searchParams.get("status")).toBe("failed");
    expect(asked.searchParams.get("event_type")).toBe("ping");
  });

  it("puts a chosen filter in the address", async () => {
    fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      [`GET ${log}`]: { status: 200, body: { items: [succeeded], next_cursor: null } },
    });
    const onSearch = renderLog();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("combobox", { name: "Status" }));
    await user.click(await screen.findByRole("option", { name: "Failed" }));
    expect(onSearch).toHaveBeenCalledWith({ tab: "deliveries", status: "failed" });
  });

  it("opens every attempt of a delivery beside the log", async () => {
    fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      [`GET ${log}`]: { status: 200, body: { items: [retrying], next_cursor: null } },
    });
    renderLog({ tab: "deliveries", delivery: retrying.id });

    const sheet = await screen.findByRole("dialog", { name: "ping" });
    expect(sheet).toHaveTextContent(retrying.event_id);
    const attempt = within(sheet).getByRole("listitem", { name: "Attempt 1" });
    expect(attempt).toHaveTextContent("No response");
    expect(attempt).toHaveTextContent("connection refused");
  });

  it("reads a delivery the log no longer lists by its identifier", async () => {
    fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      [`GET ${log}`]: { status: 200, body: { items: [], next_cursor: null } },
      [`GET ${log}/${succeeded.id}`]: { status: 200, body: succeeded },
    });
    renderLog({ tab: "deliveries", delivery: succeeded.id });

    const sheet = await screen.findByRole("dialog", { name: "flag.kill_switch" });
    expect(within(sheet).getByText("received")).toBeInTheDocument();
  });

  it("sends a delivery's event again", async () => {
    const server = fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      [`GET ${log}`]: { status: 200, body: { items: [succeeded], next_cursor: null } },
      [`POST ${log}/${succeeded.id}/redeliver`]: {
        status: 202,
        body: { ...succeeded, id: "0192a000-0000-7000-8000-0000000000a3", status: "pending" },
      },
    });
    renderLog();
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Redeliver the flag.kill_switch delivery" }),
    );
    await waitFor(() =>
      expect(server.requests.some((r) => r.url.endsWith("/redeliver"))).toBe(true),
    );
  });

  it("loads the next page when the server has one", async () => {
    const server = fakeServer({
      "GET /webhook-event-types": { status: 200, body: catalogue },
      [`GET ${log}`]: (request) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        return cursor
          ? { status: 200, body: { items: [succeeded], next_cursor: null } }
          : { status: 200, body: { items: [retrying], next_cursor: "c2" } };
      },
    });
    renderLog();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Load older deliveries" }));
    expect(await screen.findByRole("row", { name: /flag\.kill_switch/ })).toBeInTheDocument();
    expect(screen.getByRole("row", { name: /ping/ })).toBeInTheDocument();
    expect(server.requests.some((r) => new URL(r.url).searchParams.get("cursor") === "c2")).toBe(
      true,
    );
    expect(screen.queryByRole("button", { name: "Load older deliveries" })).toBeNull();
  });
});
