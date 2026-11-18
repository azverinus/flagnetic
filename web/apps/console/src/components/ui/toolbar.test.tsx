import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { activateForTests, renderWithProviders, renderWithRouter } from "@/test/render";

import { ContextPanel, ContextPanelProvider, PanelGroup } from "./context-panel";
import { Toolbar } from "./toolbar";

beforeEach(() => {
  activateForTests("en");
  localStorage.clear();
});

const noop = () => {};

function narrow(width: number) {
  window.matchMedia = ((query: string) => {
    const max = /max-width:\s*(\d+)px/.exec(query);
    const min = /min-width:\s*(\d+)px/.exec(query);
    return {
      matches: max ? width <= Number(max[1]) : min ? width >= Number(min[1]) : false,
      media: query,
      addEventListener: noop,
      removeEventListener: noop,
    } as unknown as MediaQueryList;
  }) as typeof window.matchMedia;
}

describe("Toolbar", () => {
  it("shows a chip for each facet in force, removable one by one or all at once", async () => {
    narrow(1440);
    const user = userEvent.setup();
    const removed = vi.fn();
    const cleared = vi.fn();
    renderWithProviders(
      <Toolbar
        chips={[
          { id: "type", label: "Type: String" },
          { id: "tag", label: "Tag: web" },
        ]}
        onRemoveChip={removed}
        onClearAll={cleared}
      />,
    );

    expect(screen.getByRole("list", { name: "Filters in force" })).toHaveTextContent("Tag: web");
    await user.click(screen.getByRole("button", { name: "Remove the filter Type: String" }));
    expect(removed).toHaveBeenCalledWith("type");
    await user.click(screen.getByRole("button", { name: "Clear all" }));
    expect(cleared).toHaveBeenCalled();
  });

  it("has no chips and no Clear all while nothing is filtered", () => {
    narrow(1440);
    renderWithProviders(<Toolbar chips={[]} onRemoveChip={noop} onClearAll={noop} />);

    expect(screen.queryByRole("list")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Clear all" })).not.toBeInTheDocument();
  });

  it("searches and sorts from the one row", async () => {
    narrow(1440);
    const user = userEvent.setup();
    const sorted = vi.fn();
    renderWithProviders(
      <Toolbar
        search={{ value: "", onChange: noop, placeholder: "Key or name" }}
        sort={{
          value: "key",
          options: [
            { value: "key", label: "Key" },
            { value: "name", label: "Name" },
          ],
          onChange: sorted,
        }}
        chips={[]}
        onRemoveChip={noop}
        onClearAll={noop}
      />,
    );

    expect(screen.getByRole("searchbox", { name: "Search" })).toHaveAttribute(
      "placeholder",
      "Key or name",
    );
    await user.click(screen.getByRole("combobox", { name: "Sort by" }));
    await user.click(screen.getByRole("option", { name: "Name" }));
    expect(sorted).toHaveBeenCalledWith("name");
  });
});

describe("the toolbar's button for the panel", () => {
  function Frame() {
    return (
      <ContextPanelProvider>
        <ContextPanel label="Views and filters" startsOpenWide>
          <PanelGroup label="Views">views</PanelGroup>
        </ContextPanel>
        <Toolbar chips={[]} onRemoveChip={noop} onClearAll={noop} />
      </ContextPanelProvider>
    );
  }

  it("is absent while a wide window shows the panel", async () => {
    narrow(1440);
    await renderWithRouter(() => <Frame />);

    expect(
      await screen.findByRole("navigation", { name: "Views and filters" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Filters" })).not.toBeInTheDocument();
  });

  it("brings the panel back where a window under 1280 px started it folded away", async () => {
    narrow(1100);
    const user = userEvent.setup();
    await renderWithRouter(() => <Frame />);

    expect(await screen.findByRole("button", { name: "Filters" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Views and filters" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Filters" }));
    expect(screen.getByRole("navigation", { name: "Views and filters" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Filters" })).not.toBeInTheDocument();
  });

  it("opens the sheet in a window under 1024 px", async () => {
    narrow(900);
    const user = userEvent.setup();
    await renderWithRouter(() => <Frame />);

    await user.click(await screen.findByRole("button", { name: "Filters" }));
    expect(screen.getByRole("dialog", { name: "Views and filters" })).toBeInTheDocument();
  });
});
