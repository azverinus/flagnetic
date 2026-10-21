import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { Can, PermissionGate } from "@/components/can";
import { Button } from "@/components/ui/button";
import { AccessProvider } from "@/components/access-provider";
import { useCan } from "@/lib/access";
import type { Permission } from "@/lib/access-names";
import { type FakeAnswer, fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

const roles = {
  items: [
    {
      name: "owner",
      permissions: [
        "project.read",
        "project.update",
        "environment.manage",
        "declaration.manage",
        "grant.manage",
        "project_token.manage",
      ],
    },
    {
      name: "maintainer",
      permissions: ["project.read", "environment.manage", "declaration.manage"],
    },
    { name: "developer", permissions: ["project.read"] },
    { name: "viewer", permissions: ["project.read"] },
  ],
};

/** A caller holding exactly these permissions in the project `checkout`. */
function holding(permissions: Permission[]) {
  return fakeServer({
    "GET /roles": { status: 200, body: roles },
    "GET /projects/checkout/me": { status: 200, body: { grants: [], permissions } },
  });
}

function Probe({ permission }: { permission: Permission }) {
  return <p>{useCan(permission) ? "may" : "may not"}</p>;
}

function inProject(ui: React.ReactNode) {
  return renderWithProviders(<AccessProvider project="checkout">{ui}</AccessProvider>);
}

beforeEach(() => activateForTests("en"));

describe("useCan", () => {
  it("answers from the permissions the caller holds in the project", async () => {
    holding(["project.read", "environment.manage"]);
    inProject(
      <>
        <Probe permission="environment.manage" />
        <Probe permission="grant.manage" />
      </>,
    );

    expect(await screen.findByText("may")).toBeInTheDocument();
    expect(screen.getByText("may not")).toBeInTheDocument();
  });
});

describe("AccessProvider", () => {
  it("shows a failed read of the roles in place of the screen, with a way to ask again", async () => {
    let failing = true;
    fakeServer({
      "GET /roles": () => (failing ? problem(500, "internal") : { status: 200, body: roles }),
      "GET /projects/checkout/me": {
        status: 200,
        body: { grants: [], permissions: ["project.read"] },
      },
    });
    inProject(
      <Can permission="environment.manage" whenMissing="disable">
        <Button>New environment</Button>
      </Can>,
    );

    const alert = await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: "New environment" })).not.toBeInTheDocument();
    failing = false;
    await userEvent.click(within(alert).getByRole("button", { name: "Try again" }));

    const action = await screen.findByRole("button", { name: "New environment" });
    expect(action).toHaveAccessibleDescription(
      "Needs the permission to manage environments, held by Owner and Maintainer.",
    );
  });

  it("keeps the screen when reading the lists again fails, with what was read before", async () => {
    let failing = false;
    const answerUntilFailing = (answer: FakeAnswer) => () =>
      failing ? problem(500, "internal") : answer;
    fakeServer({
      "GET /roles": answerUntilFailing({ status: 200, body: roles }),
      "GET /projects/checkout/me": answerUntilFailing({
        status: 200,
        body: { grants: [], permissions: ["project.read"] },
      }),
    });
    const { queryClient } = inProject(
      <Can permission="environment.manage" whenMissing="disable">
        <Button>New environment</Button>
      </Can>,
    );
    const action = await screen.findByRole("button", { name: "New environment" });

    failing = true;
    await act(async () => {
      await queryClient.refetchQueries();
      // The observers hear of the failure on the next tick.
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(queryClient.getQueryState(["roles"])?.status).toBe("error");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(action).toBeInTheDocument();
    expect(action).toHaveAccessibleDescription(
      "Needs the permission to manage environments, held by Owner and Maintainer.",
    );
  });
});

describe("Can", () => {
  it("leaves an action the caller may take untouched", async () => {
    holding(["project.read", "environment.manage"]);
    const onClick = vi.fn();
    inProject(
      <Can permission="environment.manage" whenMissing="disable">
        <Button onClick={onClick}>New environment</Button>
      </Can>,
    );

    const action = await screen.findByRole("button", { name: "New environment" });
    expect(action).not.toHaveAttribute("aria-disabled");
    expect(action).not.toHaveAccessibleDescription();
    await userEvent.click(action);
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("hides an action the caller may not take when asked to", async () => {
    holding(["project.read"]);
    inProject(
      <>
        <Probe permission="project.read" />
        <Can permission="environment.manage" whenMissing="hide">
          <Button>Delete staging</Button>
        </Can>
      </>,
    );

    await screen.findByText("may");
    expect(screen.queryByRole("button", { name: "Delete staging" })).not.toBeInTheDocument();
  });

  it("disables it otherwise, naming the permission and the roles holding it", async () => {
    holding(["project.read"]);
    const onClick = vi.fn();
    inProject(
      <Can permission="environment.manage" whenMissing="disable">
        <Button onClick={onClick}>New environment</Button>
      </Can>,
    );

    const action = await screen.findByRole("button", { name: "New environment" });
    expect(action).toHaveAttribute("aria-disabled", "true");
    await waitFor(() =>
      expect(action).toHaveAccessibleDescription(
        "Needs the permission to manage environments, held by Owner and Maintainer.",
      ),
    );
    await userEvent.click(action, { pointerEventsCheck: 0 });
    expect(onClick).not.toHaveBeenCalled();
  });

  it("shows the reason as a tooltip on hover and on focus, and Escape puts it away", async () => {
    holding(["project.read"]);
    inProject(
      <Can permission="environment.manage" whenMissing="disable">
        <Button>New environment</Button>
      </Can>,
    );
    const action = await screen.findByRole("button", { name: "New environment" });
    await waitFor(() => expect(action).toHaveAccessibleDescription(/held by Owner/));
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    await userEvent.hover(action.parentElement!);
    expect(screen.getByRole("tooltip")).toHaveTextContent(
      "Needs the permission to manage environments, held by Owner and Maintainer.",
    );
    await userEvent.unhover(action.parentElement!);
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    await userEvent.tab();
    expect(action).toHaveFocus();
    expect(screen.getByRole("tooltip")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });

  it("does not follow a disabled link", async () => {
    holding(["project.read"]);
    inProject(
      <Can permission="declaration.manage" whenMissing="disable">
        <a href="#declare">Declare</a>
      </Can>,
    );

    const link = await screen.findByRole("link", { name: "Declare" });
    expect(link).toHaveAttribute("aria-disabled", "true");
    const event = new MouseEvent("click", { bubbles: true, cancelable: true });
    link.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
  });

  it("speaks the member's language", async () => {
    activateForTests("ru");
    holding(["project.read"]);
    inProject(
      <Can permission="environment.manage" whenMissing="disable">
        <Button>New environment</Button>
      </Can>,
    );

    const action = await screen.findByRole("button", { name: "New environment" });
    await waitFor(() =>
      expect(action).toHaveAccessibleDescription(
        "Нужно право управлять окружениями, оно есть у ролей Владелец и Сопровождающий.",
      ),
    );
  });
});

describe("PermissionGate", () => {
  it("renders the screen for a caller holding the permission", async () => {
    holding(["project.read", "project.update"]);
    inProject(
      <PermissionGate permission="project.update" title="Settings">
        <p>The settings form</p>
      </PermissionGate>,
    );

    expect(await screen.findByText("The settings form")).toBeInTheDocument();
  });

  it("says what the screen would take instead of rendering it", async () => {
    holding(["project.read"]);
    inProject(
      <PermissionGate permission="project.update" title="Settings">
        <p>The settings form</p>
      </PermissionGate>,
    );

    expect(await screen.findByRole("heading", { name: "Settings", level: 1 })).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("You cannot open this screen");
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "Needs the permission to edit the project, held by Owner.",
      ),
    );
    expect(screen.queryByText("The settings form")).not.toBeInTheDocument();
  });
});
