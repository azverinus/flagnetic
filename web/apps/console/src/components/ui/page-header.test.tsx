import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Button } from "./button";
import { DropdownMenuItem } from "./dropdown-menu";
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
    expect(actions).toHaveClass("flex-wrap", "[&>*]:h-8");
  });
});

describe("a page header's actions", () => {
  it("puts the rarer actions behind one More actions button", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <PageHeader
        title="Flags"
        actions={<Button>New flag</Button>}
        more={<DropdownMenuItem>Archive</DropdownMenuItem>}
      />,
    );

    expect(screen.queryByRole("menuitem", { name: "Archive" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect(screen.getByRole("menuitem", { name: "Archive" })).toBeInTheDocument();
  });

  it("shows no More actions button without rarer actions", () => {
    renderWithProviders(<PageHeader title="Flags" actions={<Button>New flag</Button>} />);

    expect(screen.queryByRole("button", { name: "More actions" })).not.toBeInTheDocument();
  });

  it("reports a second filled button", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    renderWithProviders(
      <PageHeader
        title="Flags"
        actions={
          <>
            <Button>One</Button>
            <Button>Two</Button>
            <Button variant="outline">Three</Button>
            <Button variant="destructive">Four</Button>
          </>
        }
      />,
    );

    expect(error).toHaveBeenCalledWith(expect.stringContaining("2 filled buttons"));
    error.mockRestore();
  });
});
