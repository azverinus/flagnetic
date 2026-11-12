import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";
import { clickhouseSink, webhookSink } from "@/test/sinks";

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
