import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { activateForTests, renderWithProviders, renderWithRouter } from "@/test/render";

import { PanelButton, PanelEnvironment, PanelFact, PanelFacts } from "./panel-parts";

beforeEach(() => activateForTests("en"));

describe("PanelEnvironment", () => {
  it("names the environment, its state and, when protected, the lock in one link", async () => {
    await renderWithRouter(() => (
      <PanelEnvironment to="/" name="Production" protected on current />
    ));
    const link = screen.getByRole("link", { name: /^Production\s*Protected\s*On$/ });
    expect(link).toHaveAttribute("aria-current", "page");
  });

  it("says Off, and nothing of protection, for an open environment that is off", async () => {
    await renderWithRouter(() => (
      <PanelEnvironment to="/" name="Staging" protected={false} on={false} current={false} />
    ));
    const link = screen.getByRole("link", { name: /^Staging\s*Off$/ });
    expect(link).not.toHaveAttribute("aria-current");
  });
});

describe("PanelButton", () => {
  it("acts as a button and never submits a form", async () => {
    const act = vi.fn();
    renderWithProviders(<PanelButton onClick={act}>Compare environments</PanelButton>);
    await userEvent.click(screen.getByRole("button", { name: "Compare environments" }));
    expect(act).toHaveBeenCalledOnce();
    expect(screen.getByRole("button")).toHaveAttribute("type", "button");
  });
});

describe("PanelFacts", () => {
  it("lists each fact as a term with its value", () => {
    renderWithProviders(
      <PanelFacts>
        <PanelFact term="Owner">Ada</PanelFact>
      </PanelFacts>,
    );
    expect(screen.getByText("Owner").tagName).toBe("DT");
    expect(screen.getByText("Ada").tagName).toBe("DD");
  });
});
