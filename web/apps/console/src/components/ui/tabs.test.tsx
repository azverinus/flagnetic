import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { renderWithRouter } from "@/test/render";

import { Tabs } from "./tabs";

const tabs = [
  { value: "general", label: "General", content: <p>General settings</p> },
  { value: "members", label: "Members", content: <p>Member list</p> },
  { value: "tokens", label: "Tokens", content: <p>Token list</p> },
];

describe("tabs", () => {
  it("opens the first tab on the plain address, with exactly one selected", async () => {
    await renderWithRouter(() => <Tabs label="Project settings" tabs={tabs} />);

    expect(screen.getByRole("tablist", { name: "Project settings" })).toBeInTheDocument();
    const selected = screen.getAllByRole("tab", { selected: true });
    expect(selected).toHaveLength(1);
    expect(selected[0]).toHaveTextContent("General");
    expect(screen.getByRole("tabpanel")).toHaveTextContent("General settings");
  });

  it("opens the tab the search parameter names, so a link can open it", async () => {
    await renderWithRouter(() => <Tabs label="Settings" tabs={tabs} />, "/?tab=tokens");

    expect(screen.getByRole("tab", { selected: true })).toHaveTextContent("Tokens");
    expect(screen.getByRole("tabpanel")).toHaveTextContent("Token list");
  });

  it("writes the opened tab into the address, and the first one out of it", async () => {
    const user = userEvent.setup();
    const { router } = await renderWithRouter(() => <Tabs label="Settings" tabs={tabs} />);

    await user.click(screen.getByRole("tab", { name: "Members" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ tab: "members" }));
    expect(screen.getByRole("tabpanel")).toHaveTextContent("Member list");

    await user.click(screen.getByRole("tab", { name: "General" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it("moves between tabs with the arrow keys", async () => {
    const user = userEvent.setup();
    await renderWithRouter(() => <Tabs label="Settings" tabs={tabs} />);

    act(() => screen.getByRole("tab", { name: "General" }).focus());
    await user.keyboard("{ArrowRight}");

    expect(screen.getByRole("tab", { name: "Members" })).toHaveFocus();
    await waitFor(() =>
      expect(screen.getByRole("tab", { selected: true })).toHaveTextContent("Members"),
    );
  });

  it("falls back to the first tab when the address names none of them", async () => {
    await renderWithRouter(() => <Tabs label="Settings" tabs={tabs} />, "/?tab=nope");

    expect(screen.getByRole("tab", { selected: true })).toHaveTextContent("General");
  });

  it("scrolls its row sideways instead of widening the page", async () => {
    await renderWithRouter(() => <Tabs label="Settings" tabs={tabs} />);

    expect(screen.getByRole("tablist").parentElement).toHaveClass("overflow-x-auto");
  });
});
