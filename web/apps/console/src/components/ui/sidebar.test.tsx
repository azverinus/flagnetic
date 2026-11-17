import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { Sidebar, SidebarInset, SidebarProvider, SidebarTrigger } from "./sidebar";

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

function Frame() {
  return (
    <SidebarProvider>
      <Sidebar>
        <a href="#flags">Flags</a>
      </Sidebar>
      <SidebarInset>
        <SidebarTrigger />
      </SidebarInset>
    </SidebarProvider>
  );
}

beforeEach(() => {
  activateForTests("en");
  localStorage.clear();
});
afterEach(() => localStorage.clear());

describe("the sidebar", () => {
  it("is the complementary landmark named Sidebar", () => {
    windowWidth(1440);
    renderWithProviders(<Frame />);

    expect(screen.getByRole("complementary", { name: "Sidebar" })).toBeInTheDocument();
  });

  it("collapses to its rail, remembers it, and expands again", async () => {
    windowWidth(1440);
    const user = userEvent.setup();
    const first = renderWithProviders(<Frame />);
    const sidebar = () => screen.getByRole("complementary", { name: "Sidebar" });
    expect(sidebar()).not.toHaveAttribute("data-collapsed");

    await user.click(screen.getByRole("button", { name: "Collapse the sidebar" }));

    expect(sidebar()).toHaveAttribute("data-collapsed");
    expect(localStorage.getItem("flagnetic.sidebar-collapsed")).toBe("true");

    // A later visit finds it collapsed.
    first.unmount();
    renderWithProviders(<Frame />);
    expect(sidebar()).toHaveAttribute("data-collapsed");

    await user.click(screen.getByRole("button", { name: "Expand the sidebar" }));
    expect(sidebar()).not.toHaveAttribute("data-collapsed");
    expect(localStorage.getItem("flagnetic.sidebar-collapsed")).toBe("false");
  });

  it("is a drawer under 768 px, opened from the header and closed with its focus returned", async () => {
    windowWidth(390);
    const user = userEvent.setup();
    renderWithProviders(<Frame />);
    expect(screen.queryByRole("complementary", { name: "Sidebar" })).not.toBeInTheDocument();

    const opener = screen.getByRole("button", { name: "Open the sidebar" });
    await user.click(opener);

    const drawer = screen.getByRole("dialog", { name: "Menu" });
    expect(drawer).toContainElement(screen.getByRole("complementary", { name: "Sidebar" }));
    expect(screen.getByRole("link", { name: "Flags" })).toBeInTheDocument();

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });

  it("does not bring a drawer back after the window was widened past it", async () => {
    let width = 390;
    const listeners = new Set<() => void>();
    window.matchMedia = ((query: string) => {
      const max = /max-width:\s*(\d+)px/.exec(query);
      return {
        get matches() {
          return max ? width <= Number(max[1]) : false;
        },
        media: query,
        addEventListener: (_: string, notify: () => void) => listeners.add(notify),
        removeEventListener: (_: string, notify: () => void) => listeners.delete(notify),
      } as unknown as MediaQueryList;
    }) as typeof window.matchMedia;
    const resize = (next: number) =>
      act(() => {
        width = next;
        listeners.forEach((notify) => notify());
      });
    const user = userEvent.setup();
    renderWithProviders(<Frame />);
    await user.click(screen.getByRole("button", { name: "Open the sidebar" }));
    expect(screen.getByRole("dialog", { name: "Menu" })).toBeInTheDocument();

    resize(1440);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    resize(390);

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps the drawer full-width even when the column was collapsed", async () => {
    windowWidth(390);
    localStorage.setItem("flagnetic.sidebar-collapsed", "true");
    const user = userEvent.setup();
    renderWithProviders(<Frame />);

    await user.click(screen.getByRole("button", { name: "Open the sidebar" }));

    expect(screen.getByRole("complementary", { name: "Sidebar" })).not.toHaveAttribute(
      "data-collapsed",
    );
  });
});
