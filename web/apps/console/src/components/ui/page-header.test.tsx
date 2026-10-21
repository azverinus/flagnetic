import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Button } from "./button";
import { PageHeader } from "./page-header";

describe("a page header", () => {
  it("is the page's heading, cut with an ellipsis and readable in full on hover", () => {
    const title = "A project with a very long name that will not fit on a phone";
    renderWithProviders(<PageHeader title={title} />);

    const heading = screen.getByRole("heading", { level: 1, name: title });
    expect(heading).toHaveClass("truncate");
    expect(heading).toHaveAttribute("title", title);
  });

  it("holds its breadcrumb, description and actions, the actions wrapping at one height", () => {
    renderWithProviders(
      <PageHeader
        title="Environments"
        breadcrumb={<nav aria-label="Breadcrumb">Checkout</nav>}
        description="Where the project's flags are served."
        actions={
          <>
            <Button variant="outline">Export</Button>
            <Button>Create environment</Button>
          </>
        }
      />,
    );

    expect(screen.getByRole("navigation", { name: "Breadcrumb" })).toBeInTheDocument();
    expect(screen.getByText("Where the project's flags are served.")).toBeInTheDocument();
    const actions = screen.getByRole("button", { name: "Export" }).parentElement!;
    expect(actions).toHaveClass("flex-wrap", "[&>*]:h-9");
  });
});
