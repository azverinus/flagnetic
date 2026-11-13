import type { Sink } from "@flagnetic/api";
import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AccessProvider } from "@/components/access-provider";
import { fakeServer } from "@/test/fake-server";
import { renderWithRouter } from "@/test/render";
import { clickhouseSink, webhookSink } from "@/test/sinks";

import { ProjectSinkBanner, SinkAttentionMarker } from "./sink-banner";

const roles = {
  items: [
    { name: "maintainer", permissions: ["project.read", "sink.manage"] },
    { name: "viewer", permissions: ["project.read"] },
  ],
};

function serve(permissions: string[], sinks: Sink[]) {
  return fakeServer({
    "GET /roles": { status: 200, body: roles },
    "GET /projects/checkout/me": { status: 200, body: { grants: [], permissions } },
    "GET /projects/checkout/sinks": { status: 200, body: { items: sinks } },
  });
}

const renderBanner = () =>
  renderWithRouter(() => (
    <AccessProvider project="checkout">
      <ProjectSinkBanner project="checkout" />
    </AccessProvider>
  ));

describe("the sinks banner", () => {
  it("names each sink that is stopped, at risk or has a gap, and why, to those who manage sinks", async () => {
    serve(
      ["project.read", "sink.manage"],
      [
        { ...webhookSink, health: "stopped", at_risk: true },
        { ...clickhouseSink, gaps: 2 },
        { ...clickhouseSink, id: "0192a000-0000-7000-8000-0000000000c9", name: "Fine" },
      ],
    );
    await renderBanner();

    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent("2 sinks need your attention.");
    expect(screen.getByRole("link", { name: "Pipeline" })).toHaveAttribute(
      "href",
      `/projects/checkout/sinks/${webhookSink.id}`,
    );
    expect(banner).toHaveTextContent("stopped, at risk of losing events");
    expect(banner).toHaveTextContent("2 undismissed gaps");
    expect(screen.queryByRole("link", { name: "Fine" })).toBeNull();
  });

  it("says nothing while every sink is well", async () => {
    const server = serve(["project.read", "sink.manage"], [webhookSink]);
    await renderBanner();
    await waitFor(() =>
      expect(server.requests.some((request) => request.url.endsWith("/sinks"))).toBe(true),
    );
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("is not shown, nor the list read, to someone who cannot manage sinks", async () => {
    const server = serve(["project.read"], [{ ...webhookSink, health: "stopped" }]);
    await renderBanner();
    await waitFor(() =>
      expect(server.requests.some((request) => request.url.endsWith("/me"))).toBe(true),
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(server.requests.some((request) => request.url.endsWith("/sinks"))).toBe(false);
  });
});

describe("the marker on the Sinks item", () => {
  it("shows while a sink needs attention, and not otherwise", async () => {
    serve(["project.read", "sink.manage"], [{ ...webhookSink, at_risk: true }]);
    const first = await renderWithRouter(() => <SinkAttentionMarker project="checkout" />);
    expect(await screen.findByText("1 sink needs your attention")).toBeInTheDocument();
    first.unmount();

    serve(["project.read", "sink.manage"], [webhookSink]);
    await renderWithRouter(() => <SinkAttentionMarker project="checkout" />);
    await waitFor(() => expect(screen.queryByText(/needs your attention/)).toBeNull());
  });
});
