import { screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { InstanceLink, ProjectNavigation } from "./navigation";
import { SidebarProvider } from "./ui/sidebar";

const owner = [
  "project.read",
  "project.update",
  "project_token.manage",
  "webhook.manage",
  "sink.manage",
  "change_request.approve",
];

function serve(permissions: string[]) {
  fakeServer({
    "GET /projects/checkout/me": { status: 200, body: { permissions } },
    "GET /projects/checkout/change-requests": { status: 200, body: { items: [] } },
    "GET /projects/checkout/sinks": { status: 200, body: { items: [] } },
    "GET /projects/checkout/webhooks": { status: 200, body: { items: [] } },
    "GET /me": {
      status: 200,
      body: { credential: { token_name: "" }, member: { instance_admin: true } },
    },
  });
}

async function renderNavigation() {
  return renderWithRouter(() => (
    <SidebarProvider>
      <ProjectNavigation project="checkout" />
      <InstanceLink />
    </SidebarProvider>
  ));
}

beforeEach(() => {
  activateForTests("en");
  localStorage.clear();
  window.matchMedia = ((query: string) =>
    ({
      matches: false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }) as unknown as MediaQueryList) as typeof window.matchMedia;
});
afterEach(() => localStorage.clear());

describe("the project navigation in the sidebar", () => {
  it("lists the daily screens, the activity ones, and the link to the project's settings", async () => {
    serve(owner);
    await renderNavigation();

    const navigation = await screen.findByRole("navigation", { name: "Project" });
    const names = within(navigation)
      .getAllByRole("link")
      .map((link) => link.textContent);
    expect(names).toEqual([
      "Overview",
      "Flags",
      "Segments",
      "Experiments",
      "Change requests",
      "Audit log",
      "Event volume",
      "Project settings",
    ]);
    expect(within(navigation).getByText("Activity")).toBeInTheDocument();
  });

  it("opens the project's settings at General for one who may edit the project", async () => {
    serve(owner);
    await renderNavigation();

    const link = await screen.findByRole("link", { name: "Project settings" });

    expect(link).toHaveAttribute("href", "/projects/checkout/settings");
  });

  it("opens the project's settings at the first screen a viewer may open", async () => {
    serve(["project.read"]);
    await renderNavigation();

    const link = await screen.findByRole("link", { name: "Project settings" });

    expect(link).toHaveAttribute("href", "/projects/checkout/import-export");
  });

  it("shows the instance link to an instance admin, apart from the project's navigation", async () => {
    serve(owner);
    await renderNavigation();

    const instance = await screen.findByRole("link", { name: "Instance" });

    expect(instance).toHaveAttribute("href", "/admin/projects");
    expect(
      within(await screen.findByRole("navigation", { name: "Project" })).queryByRole("link", {
        name: "Instance",
      }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Instance" })).not.toBeInTheDocument();
  });
});
