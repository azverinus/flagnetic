import type { Experiment } from "@flagnetic/api";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it } from "vitest";

import { AccessProvider } from "@/components/access-provider";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { ExperimentTransitions } from "./experiment-transitions";

beforeAll(() => activateForTests("en"));

async function show(
  status: Experiment["status"],
  permissions = ["experiment.edit", "experiment.delete"],
) {
  fakeServer({
    "GET /roles": { status: 200, body: { items: [] } },
    "GET /projects/shop/me": { status: 200, body: { grants: [], permissions } },
    "GET /projects/shop/flags/checkout": {
      status: 200,
      body: { key: "checkout", archived_at: null, variations: [], environments: [] },
    },
    "GET /projects/shop/environments": { status: 200, body: { items: [] } },
  });
  const experiment = {
    key: "cta_copy",
    flag: "checkout",
    status,
    variations: [],
  } as unknown as Experiment;
  await renderWithRouter(() => (
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

const menuItems = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "More actions" }));
  return screen.getAllByRole("menuitem").map((item) => item.textContent);
};

describe("the experiment's header actions", () => {
  it("fills only Start on a draft and keeps archive, clone and delete in the menu", async () => {
    await show("draft");
    expect(await screen.findByRole("button", { name: "Start" })).toHaveAttribute(
      "data-variant",
      "default",
    );
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    expect(await menuItems()).toEqual(["Clone", "Archive", "Delete"]);
  });

  it("fills only Stop on a running experiment and keeps growth and clone in the menu", async () => {
    await show("running");
    expect(await screen.findByRole("button", { name: "Stop" })).toHaveAttribute(
      "data-variant",
      "default",
    );
    expect(await menuItems()).toEqual(["Grow traffic allocation", "Clone"]);
  });

  it("fills Archive on a stopped experiment and leaves Clone in the menu", async () => {
    await show("stopped");
    expect(await screen.findByRole("button", { name: "Archive" })).toHaveAttribute(
      "data-variant",
      "default",
    );
    expect(await menuItems()).toEqual(["Clone"]);
  });

  it("offers an archived experiment only the menu's Clone", async () => {
    await show("archived");
    expect(await menuItems()).toEqual(["Clone"]);
  });

  it("marks what the caller may not do as disabled, with the reason, in the menu", async () => {
    await show("draft", []);
    await menuItems();
    expect(screen.getByRole("menuitem", { name: "Delete" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
  });
});
