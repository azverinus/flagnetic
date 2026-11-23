import type { Experiment, Flag, Project, Segment } from "@flagnetic/api";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import {
  attributesQuery,
  projectQuery,
  experimentQuery,
  segmentQuery,
  segmentsQuery,
  unitTypesQuery,
} from "@/lib/queries";
import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderRoute } from "@/test/render";

// The first render of a route loads the whole route tree, which on a loaded
// runner takes longer than a test is allowed; loading it here keeps that cost
// out of whichever test happens to run first.
beforeAll(async () => {
  activateForTests("en");
  await import("@/routeTree.gen");
}, 60_000);

const project: Project = {
  id: "0192a000-0000-7000-8000-000000000100",
  key: "checkout",
  name: "Checkout",
  description: "",
  member_count: 1,
  version: 1,
};

const segment: Segment = {
  id: "0192a000-0000-7000-8000-000000000200",
  key: "vip",
  name: "VIP",
  description: "",
  private: false,
  conditions: [],
  created_at: "2026-10-25T10:00:00Z",
  updated_at: "2026-10-25T10:00:00Z",
  version: 1,
};

const experiment: Experiment = {
  id: "0192a000-0000-7000-8000-000000000300",
  key: "new-checkout",
  name: "New checkout",
  hypothesis: "Fewer steps convert better",
  owner: "ada",
  tags: [],
  flag: "checkout-flow",
  unit_type: "user",
  variations: [
    { variation: "control", weight: 5000 },
    { variation: "treatment", weight: 5000 },
  ],
  traffic_allocation: 10000,
  inclusion_salt: "i",
  variation_salt: "v",
  status: "draft",
  expected_end_at: null,
  started_at: null,
  stopped_at: null,
  archived_at: null,
  decision: null,
  created_by: "ada",
  created_at: "2026-10-25T10:00:00Z",
  updated_at: "2026-10-25T10:00:00Z",
  version: 1,
};

const permissions = [
  "segment.edit",
  "experiment.edit",
  "project.update",
  "flag_configuration.update",
];

/** A read a page makes that the test does not look at: nothing there. */
const absent = problem(404, "not_found");

/** What every page under the project reads, answered with nothing in particular. */
const shell = {
  "GET /projects/checkout": { status: 200, body: project },
  "GET /projects/checkout/me": { status: 200, body: { grants: [], permissions } },
  "GET /roles": { status: 200, body: { items: [] } },
  "GET /projects/checkout/attributes": { status: 200, body: { items: [] } },
  "GET /projects/checkout/unit-types": { status: 200, body: { items: [] } },
  "GET /projects/checkout/segments": { status: 200, body: { items: [] } },
  "GET /projects/checkout/segments/vip/usages": absent,
  "GET /projects/checkout/environments": absent,
  "GET /projects/checkout/members": absent,
  "GET /projects/checkout/flags/checkout-flow": absent,
  "GET /projects/checkout/experiments/new-checkout/alerts": absent,
  "GET /projects/checkout/change-requests": absent,
  "GET /projects/checkout/flags/checkout-flow/environments/production/evaluations": absent,
};

describe("a segment's page", () => {
  const route = "GET /projects/checkout/segments/vip";

  it("keeps the editor and its draft when a read it depends on fails after it held data", async () => {
    let failing = false;
    fakeServer({
      ...shell,
      [route]: { status: 200, body: segment },
      "GET /projects/checkout/attributes": () =>
        failing ? problem(500, "internal") : { status: 200, body: { items: [] } },
    });
    const { queryClient } = await renderRoute("/projects/checkout/segments/vip");
    const user = userEvent.setup();

    const name = await screen.findByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Very important");
    failing = true;
    await act(() => queryClient.refetchQueries({ queryKey: attributesQuery("checkout").queryKey }));
    await waitFor(() =>
      expect(queryClient.getQueryState(attributesQuery("checkout").queryKey)?.status).toBe("error"),
    );
    // Let the page render what the failed read left.
    await act(async () => {});

    expect(screen.getByLabelText("Name")).toHaveValue("Very important");
  });

  it("starts a clean page over from a newer version its query answers", async () => {
    fakeServer({ ...shell, [route]: { status: 200, body: segment } });
    const { queryClient } = await renderRoute("/projects/checkout/segments/vip");

    expect(await screen.findByLabelText("Name")).toHaveValue("VIP");
    act(() => {
      queryClient.setQueryData(segmentQuery("checkout", "vip").queryKey, {
        ...segment,
        name: "Very important",
        version: 2,
      });
    });

    await waitFor(() => expect(screen.getByLabelText("Name")).toHaveValue("Very important"));
  });
});

describe("an experiment's page", () => {
  const route = "GET /projects/checkout/experiments/new-checkout";
  const flag = {
    id: "0192a000-0000-7000-8000-000000000400",
    key: "checkout-flow",
    name: "Checkout flow",
    archived_at: null,
    variations: [{ key: "control" }, { key: "treatment" }],
  };
  const reads = {
    ...shell,
    "GET /projects/checkout/flags": { status: 200, body: { items: [flag], next_cursor: null } },
    "GET /projects/checkout/unit-types": {
      status: 200,
      body: { items: [{ key: "user", name: "User" }] },
    },
  };

  it("keeps the editor and its draft when a read it depends on fails after it held data", async () => {
    let failing = false;
    fakeServer({
      ...reads,
      [route]: { status: 200, body: experiment },
      "GET /projects/checkout/unit-types": () =>
        failing
          ? problem(500, "internal")
          : { status: 200, body: { items: [{ key: "user", name: "User" }] } },
    });
    const { queryClient } = await renderRoute("/projects/checkout/experiments/new-checkout");
    const user = userEvent.setup();

    const name = await screen.findByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Renamed");
    failing = true;
    await act(() => queryClient.refetchQueries({ queryKey: unitTypesQuery("checkout").queryKey }));
    await waitFor(() =>
      expect(queryClient.getQueryState(unitTypesQuery("checkout").queryKey)?.status).toBe("error"),
    );
    // Let the page render what the failed read left.
    await act(async () => {});

    expect(screen.getByLabelText("Name")).toHaveValue("Renamed");
  });

  it("shows the new status of a clean page when another member started the experiment", async () => {
    fakeServer({ ...reads, [route]: { status: 200, body: experiment } });
    const { queryClient } = await renderRoute("/projects/checkout/experiments/new-checkout");

    expect(await screen.findByRole("button", { name: "Start" })).toBeVisible();
    act(() => {
      queryClient.setQueryData(experimentQuery("checkout", "new-checkout").queryKey, {
        ...experiment,
        status: "running",
        started_at: "2026-10-26T10:00:00Z",
        version: 2,
      });
    });

    await waitFor(() => expect(screen.queryByRole("button", { name: "Start" })).toBeNull());
  });
});

describe("a flag's page", () => {
  const flag: Flag = {
    id: "0192a000-0000-7000-8000-000000000500",
    key: "checkout-flow",
    type: "string",
    name: "Checkout flow",
    description: "",
    owner: "ada",
    tags: [],
    lifetime: "permanent",
    expected_removal_at: null,
    automatic_exposure: false,
    variations: [
      { key: "control", value: "a", name: "Control", description: "" },
      { key: "treatment", value: "b", name: "Treatment", description: "" },
    ],
    archived_at: null,
    created_by: null,
    created_at: "2026-10-25T10:00:00Z",
    updated_at: "2026-10-25T10:00:00Z",
    version: 1,
    last_evaluated: [],
    environments: [
      {
        environment: "production",
        on: true,
        off_variation: "control",
        fallthrough: "control",
        prerequisites: [],
        overrides: [],
        rules: [],
        updated_at: "2026-10-25T10:00:00Z",
        updated_by: null,
        version: 1,
      },
    ],
  };

  it("keeps the configuration editor and its draft when a read it depends on fails", async () => {
    let failing = false;
    fakeServer({
      ...shell,
      "GET /projects/checkout/flags/checkout-flow": { status: 200, body: flag },
      "GET /projects/checkout/environments": {
        status: 200,
        body: {
          items: [
            {
              id: "0192a000-0000-7000-8000-000000000600",
              key: "production",
              name: "Production",
              sort_order: 1,
              version: 1,
              protected: false,
            },
          ],
        },
      },
      "GET /projects/checkout/flags": { status: 200, body: { items: [], next_cursor: null } },
      "GET /projects/checkout/experiments": {
        status: 200,
        body: { items: [], next_cursor: null },
      },
      "GET /projects/checkout/segments": () =>
        failing ? problem(500, "internal") : { status: 200, body: { items: [] } },
    });
    const { queryClient } = await renderRoute(
      "/projects/checkout/flags/checkout-flow?environment=production",
    );
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Add rule" }));
    const rules = screen.getAllByRole("button", { name: /Remove rule|Delete rule/ }).length;
    failing = true;
    await act(() => queryClient.refetchQueries({ queryKey: segmentsQuery("checkout").queryKey }));
    await waitFor(() =>
      expect(queryClient.getQueryState(segmentsQuery("checkout").queryKey)?.status).toBe("error"),
    );
    await act(async () => {});

    expect(screen.getAllByRole("button", { name: /Remove rule|Delete rule/ })).toHaveLength(rules);
    expect(rules).toBeGreaterThan(0);
  });
});

describe("a project's settings", () => {
  it("starts a clean form over from a newer version its query answers", async () => {
    fakeServer({ ...shell });
    const { queryClient } = await renderRoute("/projects/checkout/settings");

    expect(await screen.findByLabelText("Name")).toHaveValue("Checkout");
    act(() => {
      queryClient.setQueryData(projectQuery("checkout").queryKey, {
        ...project,
        name: "Checkout team",
        version: 2,
      });
    });

    await waitFor(() => expect(screen.getByLabelText("Name")).toHaveValue("Checkout team"));
  });
});
