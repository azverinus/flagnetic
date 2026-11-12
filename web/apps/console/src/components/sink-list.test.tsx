import type { Sink } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";
import { clickhouseSink, webhookSink } from "@/test/sinks";

import { SinkList } from "./sink-list";

const list = "/projects/checkout/sinks";

const running: Sink = {
  ...webhookSink,
  enabled: true,
  health: "lagging",
  at_risk: true,
  lag_seconds: 7_500,
  streams: [
    { event_kind: "exposure", last_delivered_at: "2026-11-02T10:00:00Z", last_failure: null },
  ],
};

function renderList() {
  return renderWithRouter(() => <SinkList project="checkout" />);
}

describe("the project's sinks", () => {
  it("list each sink with its kind, health, last delivery, lag and risk", async () => {
    fakeServer({ [`GET ${list}`]: { status: 200, body: { items: [clickhouseSink, running] } } });
    await renderList();

    const warehouse = await screen.findByRole("row", { name: /Warehouse/ });
    expect(warehouse).toHaveTextContent("ClickHouse");
    expect(warehouse).toHaveTextContent("Disabled");
    expect(warehouse).toHaveTextContent("Never");

    const pipeline = screen.getByRole("row", { name: /Pipeline/ });
    expect(pipeline).toHaveTextContent("Webhook");
    expect(pipeline).toHaveTextContent("Yes");
    expect(pipeline).toHaveTextContent("Lagging");
    expect(pipeline).toHaveTextContent("2 hours");
    expect(pipeline).toHaveTextContent("At risk");
    expect(within(pipeline).getByRole("link", { name: "Pipeline" })).toBeInTheDocument();
  });

  it("offer the first sink when there is none", async () => {
    fakeServer({ [`GET ${list}`]: { status: 200, body: { items: [] } } });
    await renderList();
    expect(await screen.findByText("No sink yet.")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "New sink" }).length).toBeGreaterThan(0);
  });

  it("delete a sink after confirming, naming the version it read", async () => {
    let items = [clickhouseSink];
    const server = fakeServer({
      [`GET ${list}`]: () => ({ status: 200, body: { items } }),
      [`DELETE ${list}/${clickhouseSink.id}`]: () => {
        items = [];
        return { status: 204 };
      },
    });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Delete Warehouse" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete Warehouse" });
    await user.click(within(dialog).getByRole("button", { name: "Delete sink" }));

    expect(await screen.findByText("No sink yet.")).toBeInTheDocument();
    const request = server.requests.find((r) => r.method === "DELETE")!;
    expect(request.headers.get("If-Match")).toBe('"1"');
  });

  it("explain a refused deletion in the dialog", async () => {
    fakeServer({
      [`GET ${list}`]: { status: 200, body: { items: [clickhouseSink] } },
      [`DELETE ${list}/${clickhouseSink.id}`]: problem(403, "forbidden", {
        permission: "sink.manage",
      }),
    });
    await renderList();
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Delete Warehouse" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete Warehouse" });
    await user.click(within(dialog).getByRole("button", { name: "Delete sink" }));
    await waitFor(() => expect(within(dialog).getByRole("alert")).toBeInTheDocument());
    expect(within(dialog).getByRole("alert")).toHaveTextContent("manage sinks");
  });
});
