import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Button } from "./button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./dropdown-menu";
import { EmptyState } from "./empty-state";
import { Field } from "./field";
import { Input } from "./input";
import { Popover, PopoverContent, PopoverTrigger } from "./popover";
import { Skeleton } from "./skeleton";
import { Switch } from "./switch";
import { Textarea } from "./textarea";

describe("a button", () => {
  it("is a button that is not a submit by accident only when told", async () => {
    const onClick = vi.fn();
    renderWithProviders(<Button onClick={onClick}>Save</Button>);

    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("renders as its child, such as a link, keeping the look", () => {
    renderWithProviders(
      <Button asChild>
        <a href="/projects">Projects</a>
      </Button>,
    );

    expect(screen.getByRole("link", { name: "Projects" })).toHaveClass("inline-flex");
  });
});

describe("a dropdown menu", () => {
  it("opens from the keyboard and runs the chosen item", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    renderWithProviders(
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button>Actions</Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem onSelect={onSelect}>Rename</DropdownMenuItem>
          <DropdownMenuItem>Delete</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    );

    screen.getByRole("button", { name: "Actions" }).focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("menu")).toBeInTheDocument();
    expect(screen.getAllByRole("menuitem")).toHaveLength(2);

    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });
});

describe("a popover", () => {
  it("opens next to its trigger and closes on Escape, giving focus back", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Popover>
        <PopoverTrigger asChild>
          <Button>Filters</Button>
        </PopoverTrigger>
        <PopoverContent aria-label="Filters">Only active</PopoverContent>
      </Popover>,
    );
    const trigger = screen.getByRole("button", { name: "Filters" });

    await user.click(trigger);
    expect(screen.getByRole("dialog", { name: "Filters" })).toHaveTextContent("Only active");
    expect(trigger).toHaveAttribute("aria-expanded", "true");

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });
});

describe("a switch", () => {
  it("says on or off and toggles with the space bar", async () => {
    const user = userEvent.setup();
    function Toggle() {
      const [on, setOn] = useState(false);
      return <Switch aria-label="Require an expiry" checked={on} onCheckedChange={setOn} />;
    }
    renderWithProviders(<Toggle />);
    const toggle = screen.getByRole("switch", { name: "Require an expiry" });

    expect(toggle).toHaveAttribute("aria-checked", "false");
    toggle.focus();
    await user.keyboard(" ");
    expect(toggle).toHaveAttribute("aria-checked", "true");
  });
});

describe("the text controls", () => {
  it("are named by their field and take what is typed", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Field label="Key">
          <Input />
        </Field>
        <Field label="Description">
          <Textarea />
        </Field>
      </>,
    );

    await user.type(screen.getByRole("textbox", { name: "Key" }), "checkout");
    await user.type(screen.getByRole("textbox", { name: "Description" }), "The shop");

    expect(screen.getByRole("textbox", { name: "Key" })).toHaveValue("checkout");
    expect(screen.getByRole("textbox", { name: "Description" })).toHaveValue("The shop");
  });
});

describe("an empty state and a skeleton", () => {
  it("says what would be here and offers the action that adds it", () => {
    renderWithProviders(
      <EmptyState
        title="No environments yet"
        description="Environments are where flags are served."
        action={<Button>Create environment</Button>}
      />,
    );

    expect(screen.getByText("No environments yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create environment" })).toBeInTheDocument();
  });

  it("keeps a placeholder out of the accessibility tree", () => {
    const { container } = renderWithProviders(<Skeleton className="h-4" />);

    expect(container.firstElementChild).toHaveAttribute("aria-hidden", "true");
  });
});
