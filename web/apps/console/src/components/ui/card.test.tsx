import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Card, CardContent, CardHeader } from "./card";
import { EmptyState } from "./empty-state";
import { SettingsSection } from "./settings-section";

describe("a card", () => {
  it("is a section with a heading, a description, actions and content", () => {
    renderWithProviders(
      <Card aria-labelledby="t">
        <CardHeader
          titleId="t"
          title="Webhooks"
          description="Where events go."
          actions={<button type="button">Add</button>}
        />
        <CardContent>Body</CardContent>
      </Card>,
    );

    expect(screen.getByRole("region", { name: "Webhooks" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Webhooks" })).toBeInTheDocument();
    expect(screen.getByText("Where events go.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
    expect(screen.getByText("Body")).toBeInTheDocument();
  });

  it("keeps a list's card at 40% of the viewport at least", () => {
    renderWithProviders(
      <>
        <Card list data-testid="list" />
        <Card data-testid="plain" />
      </>,
    );

    expect(screen.getByTestId("list")).toHaveClass("min-h-[40dvh]");
    expect(screen.getByTestId("plain")).not.toHaveClass("min-h-[40dvh]");
  });

  it("holds an empty state without a dashed border of its own", () => {
    renderWithProviders(
      <Card list>
        <EmptyState title="Nothing yet" action={<button type="button">Create</button>} />
      </Card>,
    );

    const empty = screen.getByText("Nothing yet").parentElement;
    expect(empty?.className).not.toMatch(/border/);
    expect(screen.getByRole("button", { name: "Create" })).toBeInTheDocument();
  });

  it("leaves an empty state outside a card its own dashed border", () => {
    renderWithProviders(<EmptyState title="Nothing yet" />);

    expect(screen.getByText("Nothing yet").parentElement).toHaveClass("border-dashed");
  });

  it("renders a settings section as a card", () => {
    renderWithProviders(
      <SettingsSection title="Profile" description="Your name.">
        Fields
      </SettingsSection>,
    );

    const section = screen.getByRole("heading", { level: 2, name: "Profile" }).closest("section");
    expect(section).toHaveClass("rounded-xl", "border", "bg-card");
    expect(screen.getByText("Fields")).toBeInTheDocument();
  });
});
