import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";
import { clickhouseSink, webhookSink } from "@/test/sinks";

import type { Sink } from "@flagnetic/api";

import { SinkScreen } from "./sink-screen";

const base = (id: string) => `/projects/checkout/sinks/${id}`;
const common = {
  "GET /projects/checkout/environments": { status: 200, body: { items: [] } },
};

describe("a sink's screen", () => {
  it("shows the sink and its settings", async () => {
    fakeServer({
      ...common,
      [`GET ${base(clickhouseSink.id)}`]: { status: 200, body: clickhouseSink },
    });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={clickhouseSink.id} />);

    expect(await screen.findByRole("heading", { name: "Warehouse", level: 1 })).toBeVisible();
    expect(screen.getByLabelText("Database")).toHaveValue("analytics");
    // Only a webhook sink has a secret to rotate.
    expect(screen.queryByRole("button", { name: "Rotate secret" })).toBeNull();
  });

  it("says the sink is missing when the server has none", async () => {
    fakeServer(common);
    await renderWithRouter(() => <SinkScreen project="checkout" sink={clickhouseSink.id} />);
    expect(await screen.findByRole("heading", { name: "Sink not found" })).toBeVisible();
  });

  it("rotates a webhook sink's secret and shows the new one once", async () => {
    const secret = "rotated-s3cret";
    fakeServer({
      ...common,
      [`GET ${base(webhookSink.id)}`]: { status: 200, body: webhookSink },
      [`POST ${base(webhookSink.id)}/rotate-secret`]: {
        status: 200,
        body: { ...webhookSink, version: 2, secret },
      },
    });
    const { queryClient } = await renderWithRouter(() => (
      <SinkScreen project="checkout" sink={webhookSink.id} />
    ));
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Rotate secret" }));
    const confirm = await screen.findByRole("dialog", { name: "Rotate the secret of Pipeline" });
    await user.click(within(confirm).getByRole("button", { name: "Rotate secret" }));

    const reveal = await screen.findByRole("dialog", { name: "The sink's signing secret" });
    expect(within(reveal).getByText(secret)).toBeInTheDocument();
    await user.click(within(reveal).getByLabelText("I have stored it somewhere safe"));
    await user.click(within(reveal).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByText(secret)).not.toBeInTheDocument());
    expect(
      JSON.stringify(
        queryClient
          .getQueryCache()
          .getAll()
          .map((q) => q.state.data),
      ),
    ).not.toContain(secret);
    expect(
      JSON.stringify(
        queryClient
          .getMutationCache()
          .getAll()
          .map((m) => m.state),
      ),
    ).not.toContain(secret);
  });
});

const enabled: Sink = {
  ...webhookSink,
  enabled: true,
  health: "failing",
  at_risk: true,
  lag_seconds: 125,
  version: 2,
  streams: [
    {
      event_kind: "exposure",
      last_delivered_at: "2026-11-02T09:00:00Z",
      last_failure: null,
    },
    {
      event_kind: "alias",
      last_delivered_at: null,
      last_failure: { class: "status", status: 502, code: null, at: "2026-11-02T10:00:00Z" },
    },
  ],
};

describe("a sink's detail", () => {
  it("shows health, lag, at risk and each stream's last delivery and failure", async () => {
    fakeServer({
      ...common,
      [`GET ${base(enabled.id)}`]: { status: 200, body: enabled },
    });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={enabled.id} />);

    expect(await screen.findByText("Failing")).toBeVisible();
    expect(screen.getByText("2 minutes")).toBeVisible();
    expect(screen.getByText("At risk", { selector: "span" })).toBeVisible();
    const row = screen.getByRole("row", { name: /Aliases/ });
    expect(
      within(row).getByText("The target answered with an error status (status 502)"),
    ).toBeVisible();
    expect(within(row).getByText("Never")).toBeVisible();
    expect(within(screen.getByRole("row", { name: /Exposures/ })).queryByText("Never")).toBeNull();
    expect(screen.getByRole("button", { name: "Disable" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Enable" })).toBeNull();
  });

  it("explains a refusal to enable inline, naming the failure, and keeps the dialog open", async () => {
    fakeServer({
      ...common,
      [`GET ${base(webhookSink.id)}`]: { status: 200, body: webhookSink },
      [`POST ${base(webhookSink.id)}/enable`]: problem(422, "sink_not_ready", {
        failure: "status",
        failure_status: 503,
      }),
    });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={webhookSink.id} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Enable" }));
    const dialog = await screen.findByRole("dialog", { name: "Enable Pipeline" });
    await user.click(within(dialog).getByRole("button", { name: "Enable sink" }));

    expect(
      await within(dialog).findByText(
        "The sink is not ready to be enabled: The target answered with an error status (status 503).",
      ),
    ).toBeVisible();
    expect(screen.getByRole("dialog", { name: "Enable Pipeline" })).toBeVisible();
  });

  it("enables from now, or from a chosen time", async () => {
    const server = fakeServer({
      ...common,
      [`GET ${base(webhookSink.id)}`]: { status: 200, body: webhookSink },
      [`POST ${base(webhookSink.id)}/enable`]: {
        status: 200,
        body: { ...webhookSink, enabled: true, health: "healthy", version: 2 },
      },
    });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={webhookSink.id} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Enable" }));
    const dialog = await screen.findByRole("dialog", { name: "Enable Pipeline" });
    await user.click(within(dialog).getByRole("radio", { name: /From a time/ }));
    await user.click(within(dialog).getByRole("button", { name: "Enable sink" }));
    expect(await within(dialog).findByText("Choose a time.")).toBeVisible();

    await user.type(within(dialog).getByLabelText("Start from"), "2026-11-01T10:30");
    await user.click(within(dialog).getByRole("button", { name: "Enable sink" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Enable Pipeline" })).toBeNull(),
    );
    const enable = server.requests.find((r) => r.url.endsWith("/enable"))!;
    expect(await enable.json()).toEqual({
      start_from: new Date("2026-11-01T10:30").toISOString(),
    });
  });

  it("disables after saying the cursor is kept", async () => {
    const server = fakeServer({
      ...common,
      [`GET ${base(enabled.id)}`]: { status: 200, body: enabled },
      [`PATCH ${base(enabled.id)}`]: {
        status: 200,
        body: { ...enabled, enabled: false, health: "disabled", version: 3 },
      },
    });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={enabled.id} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Disable" }));
    const dialog = await screen.findByRole("dialog", { name: "Disable Pipeline" });
    expect(within(dialog).getByText(/keeps its place in the event stream/)).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "Disable sink" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Disable Pipeline" })).toBeNull(),
    );
    const patch = server.requests.find((r) => r.method === "PATCH")!;
    expect(await patch.json()).toEqual({ enabled: false });
    expect(patch.headers.get("If-Match")).toBe('"2"');
    expect(await screen.findByRole("button", { name: "Enable" })).toBeVisible();
  });

  it("sends a sample and shows each kind's result, a failure by class and status", async () => {
    const server = fakeServer({
      ...common,
      [`GET ${base(webhookSink.id)}`]: { status: 200, body: webhookSink },
      [`POST ${base(webhookSink.id)}/sample`]: {
        status: 200,
        body: {
          results: [
            { event_kind: "exposure", ok: true, class: null, status: null, code: null },
            { event_kind: "alias", ok: false, class: "status", status: 500, code: null },
            { event_kind: "evaluation_summary", ok: true, class: null, status: null, code: null },
          ],
        },
      },
    });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={webhookSink.id} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Send sample" }));
    const dialog = await screen.findByRole("dialog", { name: "Send a sample to Pipeline" });
    await user.click(within(dialog).getByRole("checkbox", { name: "Evaluation summaries" }));
    await user.click(within(dialog).getByRole("button", { name: "Send sample" }));

    expect(
      await within(dialog).findByText("The target answered with an error status (status 500)"),
    ).toBeVisible();
    const sent = server.requests.find((r) => r.url.endsWith("/sample"))!;
    expect(await sent.json()).toEqual({ event_kinds: ["exposure", "alias"] });
  });

  it("counts the dead letters and the gaps in their tabs, and keeps the settings in the first", async () => {
    const sink: Sink = { ...webhookSink, health: "degraded", dead_letters: 2, gaps: 1 };
    fakeServer({ ...common, [`GET ${base(sink.id)}`]: { status: 200, body: sink } });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={sink.id} />);

    expect(await screen.findByRole("tab", { name: "Settings" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("tab", { name: "Dead letters (2)" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "Gaps (1)" })).toBeVisible();
    expect(screen.getByRole("tab", { name: "Replay" })).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("Events were lost");
  });

  it("offers to resume a stopped sink", async () => {
    const sink: Sink = { ...webhookSink, health: "stopped", stopped_at: "2026-11-01T10:00:00Z" };
    fakeServer({ ...common, [`GET ${base(sink.id)}`]: { status: 200, body: sink } });
    await renderWithRouter(() => <SinkScreen project="checkout" sink={sink.id} />);
    expect(await screen.findByRole("button", { name: "Resume" })).toBeVisible();
  });
});
