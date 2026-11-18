import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Page } from "./page";

describe("a page", () => {
  it("fills the width up to 1600 px, left-aligned, 24 px padded and 20 px between blocks", () => {
    renderWithProviders(<Page data-testid="page">content</Page>);

    const page = screen.getByTestId("page");
    expect(page).toHaveAttribute("data-measure", "full");
    expect(page).toHaveClass("max-w-[1600px]", "p-6", "max-sm:p-4", "gap-5", "flex-col");
    expect(page).not.toHaveClass("mx-auto");
  });

  it("keeps a 672 px reading measure for a form", () => {
    renderWithProviders(
      <Page measure="form" data-testid="page">
        content
      </Page>,
    );

    const page = screen.getByTestId("page");
    expect(page).toHaveAttribute("data-measure", "form");
    expect(page).toHaveClass("max-w-2xl");
    expect(page).not.toHaveClass("max-w-[1600px]");
  });

  it("leaves room for the tester, or for the screen below a banner", () => {
    renderWithProviders(
      <>
        <Page reserve data-testid="reserved" />
        <Page band data-testid="band" />
      </>,
    );

    expect(screen.getByTestId("reserved")).toHaveClass("pb-[62dvh]");
    expect(screen.getByTestId("band")).toHaveClass("pb-0");
  });

  it("passes through what a landmark or a busy state needs", () => {
    renderWithProviders(<Page aria-busy>loading</Page>);

    expect(screen.getByText("loading")).toHaveAttribute("aria-busy", "true");
  });
});
