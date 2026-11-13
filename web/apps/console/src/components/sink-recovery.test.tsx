import type { Sink, SinkDeadLetter, SinkGap } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";
import { webhookSink } from "@/test/sinks";

import { DeadLettersTab, GapsTab, ReplayTab, SinkAlerts } from "./sink-recovery";

const base = `/projects/checkout/sinks/${webhookSink.id}`;

const failure = { class: "rejected" as const, status: 422, code: null, at: "2026-11-01T10:05:00Z" };
const letter: SinkDeadLetter = {
  id: "0192a000-0000-7000-8000-0000000000d1",
  event_kind: "exposure",
  rows: 500,
  first_received_at: "2026-11-01T10:00:00Z",
  last_received_at: "2026-11-01T10:04:00Z",
  attempts: 5,
  last_failure: failure,
  status: "pending",
  created_at: "2026-11-01T10:05:00Z",
  redelivered_at: null,
};
const gap: SinkGap = {
  id: "0192a000-0000-7000-8000-0000000000e1",
  event_kind: "alias",
  rows: 1200,
  period_start: "2026-10-01T00:00:00Z",
  period_end: "2026-10-01T23:00:00Z",
  dropped_at: "2026-11-01T00:00:00Z",
  dismissed_at: null,
};

describe("the dead letters of a sink", () => {
  it("lists each batch with its kind, rows, attempts, last failure and status", async () => {
    fakeServer({ [`GET ${base}/dead-letters`]: { status: 200, body: { items: [letter] } } });
    await renderWithRouter(() => <DeadLettersTab project="checkout" sink={webhookSink} />);

    const row = await screen.findByRole("row", { name: /Exposures/ });
    expect(row).toHaveTextContent("500");
    expect(row).toHaveTextContent("The target rejected the batch (status 422)");
    expect(row).toHaveTextContent("Pending");
    expect(within(row).getAllByRole("cell")[3]).toHaveTextContent("5");
  });

  it("says when there are none", async () => {
    fakeServer({ [`GET ${base}/dead-letters`]: { status: 200, body: { items: [] } } });
    await renderWithRouter(() => <DeadLettersTab project="checkout" sink={webhookSink} />);
    expect(await screen.findByText("No dead letters.")).toBeVisible();
  });

  it("redelivers a dead letter and reads the list again", async () => {
    let items: SinkDeadLetter[] = [letter];
    const server = fakeServer({
      [`GET ${base}/dead-letters`]: () => ({ status: 200, body: { items } }),
      [`POST ${base}/dead-letters/${letter.id}/redeliver`]: () => {
        items = [{ ...letter, status: "redelivered", redelivered_at: "2026-11-01T11:00:00Z" }];
        return { status: 200, body: items[0] };
      },
    });
    await renderWithRouter(() => <DeadLettersTab project="checkout" sink={webhookSink} />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Redeliver the Exposures dead letter" }),
    );
    expect(await screen.findByText("Redelivered")).toBeVisible();
    expect(server.requests.filter((r) => r.method === "POST")).toHaveLength(1);
  });

  it("says a target rejected the redelivery again, leaving the dead letter pending", async () => {
    fakeServer({
      [`GET ${base}/dead-letters`]: { status: 200, body: { items: [letter] } },
      [`POST ${base}/dead-letters/${letter.id}/redeliver`]: {
        status: 200,
        body: { ...letter, attempts: 6 },
      },
    });
    await renderWithRouter(() => <DeadLettersTab project="checkout" sink={webhookSink} />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Redeliver the Exposures dead letter" }),
    );
    expect(await screen.findByText("The target rejected the dead letter again.")).toBeVisible();
  });

  it("offers no redelivery of a dead letter whose rows are gone", async () => {
    fakeServer({
      [`GET ${base}/dead-letters`]: {
        status: 200,
        body: { items: [{ ...letter, status: "expired" }] },
      },
    });
    await renderWithRouter(() => <DeadLettersTab project="checkout" sink={webhookSink} />);
    await screen.findByText("Expired");
    expect(screen.queryByRole("button", { name: /Redeliver/ })).toBeNull();
    expect(screen.getByRole("button", { name: /Discard/ })).toBeVisible();
  });

  it("discards only after confirming what is lost", async () => {
    let items: SinkDeadLetter[] = [letter];
    const server = fakeServer({
      [`GET ${base}/dead-letters`]: () => ({ status: 200, body: { items } }),
      [`DELETE ${base}/dead-letters/${letter.id}`]: () => {
        items = [];
        return { status: 204 };
      },
    });
    await renderWithRouter(() => <DeadLettersTab project="checkout" sink={webhookSink} />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Discard the Exposures dead letter" }),
    );
    const confirm = await screen.findByRole("dialog", { name: "Discard this dead letter" });
    expect(server.requests.some((r) => r.method === "DELETE")).toBe(false);
    await user.click(within(confirm).getByRole("button", { name: "Discard dead letter" }));
    await waitFor(() => expect(screen.getByText("No dead letters.")).toBeVisible());
    expect(server.requests.some((r) => r.method === "DELETE")).toBe(true);
  });

  it("explains a redelivery whose rows retention dropped", async () => {
    fakeServer({
      [`GET ${base}/dead-letters`]: { status: 200, body: { items: [letter] } },
      [`POST ${base}/dead-letters/${letter.id}/redeliver`]: problem(422, "rows_expired"),
    });
    await renderWithRouter(() => <DeadLettersTab project="checkout" sink={webhookSink} />);
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Redeliver the Exposures dead letter" }),
    );
    expect(
      await screen.findByText(/Retention has dropped every row of this dead letter/),
    ).toBeVisible();
  });
});

describe("the gaps of a sink", () => {
  it("lists them with the rows lost and dismisses one", async () => {
    let items: SinkGap[] = [gap];
    fakeServer({
      [`GET ${base}/gaps`]: () => ({ status: 200, body: { items } }),
      [`POST ${base}/gaps/${gap.id}/dismiss`]: () => {
        items = [{ ...gap, dismissed_at: "2026-11-02T09:00:00Z" }];
        return { status: 200, body: items[0] };
      },
    });
    await renderWithRouter(() => <GapsTab project="checkout" sink={webhookSink} />);
    const user = userEvent.setup();

    const row = await screen.findByRole("row", { name: /Aliases/ });
    expect(row).toHaveTextContent("1,200");
    await user.click(within(row).getByRole("button", { name: "Dismiss the Aliases gap" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: /Dismiss/ })).toBeNull());
    // A dismissed gap stays listed.
    expect(screen.getByRole("row", { name: /Aliases/ })).toBeVisible();
  });

  it("says when nothing was lost", async () => {
    fakeServer({ [`GET ${base}/gaps`]: { status: 200, body: { items: [] } } });
    await renderWithRouter(() => <GapsTab project="checkout" sink={webhookSink} />);
    expect(await screen.findByText("No events were lost.")).toBeVisible();
  });
});

describe("the replay of a sink", () => {
  async function renderReplay(sink: Sink = webhookSink, onReplayed = (_: Sink) => {}) {
    await renderWithRouter(() => (
      <ReplayTab project="checkout" sink={sink} onReplayed={onReplayed} />
    ));
    return userEvent.setup();
  }

  it("asks for a start before it asks to confirm", async () => {
    const user = await renderReplay();
    await user.click(screen.getByRole("button", { name: "Replay…" }));
    expect(await screen.findByText("Choose a time.")).toBeVisible();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("refuses an end before the start", async () => {
    const user = await renderReplay();
    await user.type(screen.getByLabelText("From"), "2026-11-02T10:00");
    await user.type(screen.getByLabelText("To"), "2026-11-01T10:00");
    await user.click(screen.getByRole("button", { name: "Replay…" }));
    expect(await screen.findByText("Choose a time after the start.")).toBeVisible();
  });

  it("confirms what the target receives again, then sends the replay", async () => {
    const replayed: Sink = { ...webhookSink, version: 2 };
    const server = fakeServer({ [`POST ${base}/replay`]: { status: 200, body: replayed } });
    let seen: Sink | undefined;
    const user = await renderReplay(webhookSink, (sink) => (seen = sink));

    await user.type(screen.getByLabelText("From"), "2026-11-01T10:00");
    await user.click(screen.getByRole("checkbox", { name: "Exposures" }));
    await user.click(screen.getByRole("button", { name: "Replay…" }));
    const confirm = await screen.findByRole("dialog", { name: "Replay Pipeline" });
    expect(confirm).toHaveTextContent("received at or after");
    expect(confirm).toHaveTextContent("Exposures");
    expect(server.requests.some((r) => r.method === "POST")).toBe(false);
    await user.click(within(confirm).getByRole("button", { name: "Replay" }));

    await waitFor(() => expect(seen).toEqual(replayed));
    const post = server.requests.find((r) => r.method === "POST")!;
    expect(await post.json()).toEqual({
      from: new Date("2026-11-01T10:00").toISOString(),
      event_kinds: ["exposure"],
    });
  });

  it("explains a start before the oldest event kept, without closing the dialog", async () => {
    fakeServer({ [`POST ${base}/replay`]: problem(422, "out_of_retention") });
    const user = await renderReplay();

    await user.type(screen.getByLabelText("From"), "2026-01-01T10:00");
    await user.click(screen.getByRole("button", { name: "Replay…" }));
    const confirm = await screen.findByRole("dialog", { name: "Replay Pipeline" });
    await user.click(within(confirm).getByRole("button", { name: "Replay" }));
    expect(await within(confirm).findByText(/before the oldest event still kept/)).toBeVisible();
  });
});

describe("what a sink needs from the maintainer", () => {
  const failing = {
    ...webhookSink,
    health: "stopped" as const,
    streams: [{ event_kind: "exposure" as const, last_delivered_at: null, last_failure: failure }],
  };

  it("shows why a stopped sink stopped and resumes it", async () => {
    const resumed: Sink = { ...failing, health: "degraded", version: 2 };
    const server = fakeServer({ [`POST ${base}/resume`]: { status: 200, body: resumed } });
    let seen: Sink | undefined;
    await renderWithRouter(() => (
      <SinkAlerts project="checkout" sink={failing} onResumed={(sink) => (seen = sink)} />
    ));
    const user = userEvent.setup();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Stopped by the platform");
    expect(alert).toHaveTextContent("Exposures stream");
    expect(alert).toHaveTextContent("status 422");
    await user.click(within(alert).getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(seen).toEqual(resumed));
    expect(server.requests.filter((r) => r.method === "POST")).toHaveLength(1);
  });

  it("warns of events about to be lost and of gaps nobody dismissed", async () => {
    await renderWithRouter(() => (
      <SinkAlerts
        project="checkout"
        sink={{ ...webhookSink, at_risk: true, gaps: 2 }}
        onResumed={() => {}}
      />
    ));
    const alerts = await screen.findAllByRole("alert");
    expect(alerts).toHaveLength(2);
    expect(alerts[0]).toHaveTextContent("At risk of losing events");
    expect(alerts[1]).toHaveTextContent("Events were lost");
    expect(screen.getByRole("link", { name: "See the gaps" })).toHaveAttribute(
      "href",
      expect.stringContaining("tab=gaps"),
    );
  });

  it("says nothing of a sink that is well", async () => {
    await renderWithRouter(() => (
      <SinkAlerts project="checkout" sink={webhookSink} onResumed={() => {}} />
    ));
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
