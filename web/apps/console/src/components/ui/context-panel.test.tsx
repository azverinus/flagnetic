import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { activateForTests, renderWithRouter } from "@/test/render";

import {
  ContextPanel,
  ContextPanelProvider,
  ContextPanelTrigger,
  useContextPanelHidden,
  PanelGroup,
  PanelLink,
} from "./context-panel";

/** A window of the given width: jsdom has no media queries of its own. */
function windowWidth(width: number) {
  window.matchMedia = ((query: string) => {
    const max = /max-width:\s*(\d+)px/.exec(query);
    return {
      matches: max ? width <= Number(max[1]) : false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    } as unknown as MediaQueryList;
  }) as typeof window.matchMedia;
}

/** The test router's second page, which its route tree does not declare to the types. */
const elsewhere = "/elsewhere" as "/";

function Frame({ declared = true }: { declared?: boolean }) {
  return (
    <ContextPanelProvider>
      <header>
        <ContextPanelTrigger />
      </header>
      {declared ? (
        <ContextPanel label="Settings">
          <PanelGroup label="Model">
            <PanelLink to="/">Environments</PanelLink>
            <PanelLink to={elsewhere} count={3}>
              Unit types
            </PanelLink>
            <PanelLink to={elsewhere} attention={<span>Needs a look</span>}>
              Sinks
            </PanelLink>
          </PanelGroup>
        </ContextPanel>
      ) : null}
      <main>page</main>
    </ContextPanelProvider>
  );
}

beforeEach(() => {
  activateForTests("en");
  localStorage.clear();
});
afterEach(() => localStorage.clear());

describe("the context panel", () => {
  it("is a navigation landmark named for what it holds, with its groups and links", async () => {
    windowWidth(1440);
    await renderWithRouter(() => <Frame />);

    const panel = await screen.findByRole("navigation", { name: "Settings" });
    expect(panel).toHaveTextContent("Model");
    expect(panel).toContainElement(screen.getByRole("link", { name: "Environments" }));
    expect(screen.getByRole("link", { name: "Environments" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("shows a count and an attention mark beside a link", async () => {
    windowWidth(1440);
    await renderWithRouter(() => <Frame />);

    expect(await screen.findByRole("link", { name: /Unit types/ })).toHaveTextContent("3");
    expect(screen.getByRole("link", { name: /Sinks/ })).toHaveTextContent("Needs a look");
  });

  it("collapses by its own toggle, remembers it, and comes back", async () => {
    windowWidth(1440);
    const user = userEvent.setup();
    const first = await renderWithRouter(() => <Frame />);
    await screen.findByRole("navigation", { name: "Settings" });

    await user.click(screen.getByRole("button", { name: "Collapse the panel" }));

    expect(screen.queryByRole("navigation", { name: "Settings" })).not.toBeInTheDocument();
    expect(localStorage.getItem("flagnetic.context-panel-collapsed")).toBe("true");

    // A later visit finds it collapsed.
    first.unmount();
    await renderWithRouter(() => <Frame />);
    expect(screen.queryByRole("navigation", { name: "Settings" })).not.toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Expand the panel" }));
    expect(screen.getByRole("navigation", { name: "Settings" })).toBeInTheDocument();
    expect(localStorage.getItem("flagnetic.context-panel-collapsed")).toBe("false");
  });

  it("has no toggle where a route declares no panel", async () => {
    windowWidth(1440);
    await renderWithRouter(() => <Frame declared={false} />);

    await screen.findByRole("main");
    expect(screen.queryByRole("button", { name: /the panel/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  });

  it("is a sheet under 1024 px, opened from the header and closed with its focus returned", async () => {
    windowWidth(1000);
    const user = userEvent.setup();
    await renderWithRouter(() => <Frame />);
    expect(screen.queryByRole("navigation", { name: "Settings" })).not.toBeInTheDocument();

    const opener = await screen.findByRole("button", { name: "Settings" });
    await user.click(opener);

    const sheet = screen.getByRole("dialog", { name: "Settings" });
    expect(sheet).toContainElement(screen.getByRole("navigation", { name: "Settings" }));

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });

  it("reaches the sheet's links by keyboard, and closes it once one has been followed", async () => {
    windowWidth(1000);
    const user = userEvent.setup();
    const { router } = await renderWithRouter(() => <Frame />);
    await screen.findByRole("button", { name: "Settings" });

    await user.tab();
    await user.keyboard("{Enter}");
    expect(screen.getByRole("dialog", { name: "Settings" })).toBeInTheDocument();
    await user.tab();
    expect(screen.getByRole("link", { name: "Environments" })).toHaveFocus();

    await act(() => router.navigate({ to: elsewhere }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("a panel link given its current state", () => {
  function Views({ current }: { current: boolean }) {
    return (
      <ContextPanelProvider>
        <ContextPanel label="Views">
          {/* Both are the page the router shows, apart from their search. */}
          <PanelLink to="/" search={{ archived: true } as never} current={current}>
            Archived
          </PanelLink>
          <PanelLink to="/" current={!current}>
            All flags
          </PanelLink>
        </ContextPanel>
      </ContextPanelProvider>
    );
  }

  it("marks the one the screen shows, whatever the router makes of the address", async () => {
    windowWidth(1440);
    await renderWithRouter(() => <Views current />);

    expect(await screen.findByRole("link", { name: "Archived" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("link", { name: "All flags" })).not.toHaveAttribute("aria-current");
  });
});

describe("a list's panel", () => {
  function List() {
    return (
      <ContextPanelProvider>
        <ContextPanel label="Filters" startsOpenWide>
          <PanelGroup>facets</PanelGroup>
        </ContextPanel>
      </ContextPanelProvider>
    );
  }

  it("remembers a choice over where the window's width starts it", async () => {
    windowWidth(1100);
    localStorage.setItem("flagnetic.context-panel-collapsed", "false");
    await renderWithRouter(() => <List />);

    expect(await screen.findByRole("navigation", { name: "Filters" })).toBeInTheDocument();
  });
});

describe("whether the panel is out of sight", () => {
  function Probe() {
    return <p>{useContextPanelHidden() ? "hidden" : "shown"}</p>;
  }
  const frame = (declared = true) => (
    <ContextPanelProvider>
      <header>
        <ContextPanelTrigger />
      </header>
      {declared ? <ContextPanel label="Settings">links</ContextPanel> : null}
      <Probe />
    </ContextPanelProvider>
  );

  it("is not while the column is open", async () => {
    windowWidth(1440);
    await renderWithRouter(() => frame());
    expect(await screen.findByText("shown")).toBeInTheDocument();
  });

  it("is once the column is folded away", async () => {
    windowWidth(1440);
    const user = userEvent.setup();
    await renderWithRouter(() => frame());
    await user.click(await screen.findByRole("button", { name: "Collapse the panel" }));
    expect(screen.getByText("hidden")).toBeInTheDocument();
  });

  it("is where the panel is a sheet", async () => {
    windowWidth(1000);
    await renderWithRouter(() => frame());
    expect(await screen.findByText("hidden")).toBeInTheDocument();
  });

  it("is not where the route declares no panel", async () => {
    windowWidth(1000);
    await renderWithRouter(() => frame(false));
    expect(await screen.findByText("shown")).toBeInTheDocument();
  });
});
