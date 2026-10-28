import type { ChangeRequestCreate, Experiment, Flag } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import { AccessProvider } from "@/components/access-provider";
import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { ExperimentTransitions } from "./experiment-transitions";

beforeAll(() => activateForTests("en"));

const experiment = {
  key: "cta_copy",
  name: "CTA copy",
  flag: "checkout",
  variations: [
    { variation: "control", weight: 5000 },
    { variation: "treatment", weight: 5000 },
  ],
  status: "running",
  traffic_allocation: 2000,
  version: 3,
} as unknown as Experiment;

const flag = {
  key: "checkout",
  archived_at: null,
  variations: [{ key: "control", name: "control" }],
  environments: [
    {
      environment: "production",
      rules: [
        {
          id: "r1",
          description: "",
          conditions: [],
          serve: { kind: "experiment", experiment: "cta_copy" },
        },
      ],
    },
  ],
} as unknown as Flag;

const created = { id: "0192a000-0000-7000-8000-0000000000aa", state: "open", version: 1 };

function protectedServer() {
  return fakeServer({
    "GET /roles": { status: 200, body: { items: [] } },
    "GET /projects/shop/me": {
      status: 200,
      body: { grants: [], permissions: ["experiment.edit", "change_request.propose"] },
    },
    "GET /projects/shop/flags/checkout": { status: 200, body: flag },
    "GET /projects/shop/environments": {
      status: 200,
      body: { items: [{ key: "production", name: "Production" }] },
    },
    "POST /projects/shop/experiments/cta_copy/stop": problem(403, "change_request_required"),
    "PATCH /projects/shop/experiments/cta_copy": problem(403, "change_request_required"),
    "POST /projects/shop/change-requests": { status: 201, body: created },
  });
}

async function renderTransitions() {
  return renderWithRouter(() => (
    <AccessProvider project="shop">
      <ExperimentTransitions
        project="shop"
        experiment={experiment}
        unitTypes={[]}
        editing={false}
        onChanged={() => {}}
      />
    </AccessProvider>
  ));
}

describe("a transition refused for a protected environment", () => {
  it("turns a stop into a proposal with the decision as it was filled in", async () => {
    const { requests } = protectedServer();
    const { router } = await renderTransitions();

    await userEvent.click(await screen.findByRole("button", { name: "Stop" }));
    const stop = screen.getByRole("dialog", { name: "Stop cta_copy" });
    await userEvent.click(within(stop).getByLabelText(/Remove: its rules are deleted/));
    await userEvent.type(within(stop).getByLabelText("Note"), "No lift.");
    await userEvent.click(within(stop).getByRole("button", { name: "Stop experiment" }));

    const propose = await screen.findByRole("dialog", { name: "Propose the change" });
    expect(screen.queryByRole("dialog", { name: "Stop cta_copy" })).not.toBeInTheDocument();
    expect(within(propose).getByRole("list", { name: "Environments changed" })).toHaveTextContent(
      "Production",
    );
    await userEvent.type(within(propose).getByLabelText("Title"), "Stop the copy test");
    await userEvent.click(within(propose).getByRole("button", { name: "Propose" }));

    await waitFor(() =>
      expect(router.state.location.pathname).toBe(`/projects/shop/change-requests/${created.id}`),
    );
    const sent = requests.find((request) => request.url.endsWith("/change-requests"));
    expect((await sent!.json()) as ChangeRequestCreate).toEqual({
      kind: "experiment",
      experiment: "cta_copy",
      proposed: { action: "stop", decision: { outcome: "removed", note: "No lift." } },
      title: "Stop the copy test",
      open: true,
    });
  });

  it("turns a growth into a proposal for the allocation asked", async () => {
    const { requests } = protectedServer();
    await renderTransitions();

    await userEvent.click(await screen.findByRole("button", { name: "Grow traffic allocation" }));
    const grow = screen.getByRole("dialog", { name: "Grow the traffic allocation of cta_copy" });
    await userEvent.type(within(grow).getByLabelText("New traffic allocation (%)"), "50");
    await userEvent.click(within(grow).getByRole("button", { name: "Grow traffic allocation" }));

    const propose = await screen.findByRole("dialog", { name: "Propose the change" });
    await userEvent.type(within(propose).getByLabelText("Title"), "Grow to half");
    await userEvent.click(within(propose).getByRole("button", { name: "Propose" }));

    await waitFor(() =>
      expect(requests.some((r) => r.url.endsWith("/change-requests"))).toBe(true),
    );
    const sent = requests.find((request) => request.url.endsWith("/change-requests"));
    expect(((await sent!.json()) as ChangeRequestCreate).proposed).toEqual({
      action: "set_traffic_allocation",
      traffic_allocation: 5000,
    });
  });
});
