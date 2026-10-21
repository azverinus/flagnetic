import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Tooltip } from "@/components/ui/tooltip";

describe("Tooltip", () => {
  it("describes its element whether or not it is shown", () => {
    render(
      <Tooltip content="Needs the permission to edit the project.">
        <button aria-describedby="hint">Rename</button>
      </Tooltip>,
    );
    const button = screen.getByRole("button", { name: "Rename" });

    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
    expect(button.getAttribute("aria-describedby")).toMatch(/^hint /);
    expect(button).toHaveAccessibleDescription("Needs the permission to edit the project.");
  });

  it("shows on hover and on focus, and Escape puts it away", async () => {
    render(
      <Tooltip content="Why not">
        <button>Rename</button>
      </Tooltip>,
    );
    const button = screen.getByRole("button", { name: "Rename" });

    await userEvent.hover(button);
    expect(screen.getByRole("tooltip")).toHaveTextContent("Why not");
    await userEvent.unhover(button);
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    await userEvent.tab();
    expect(button).toHaveFocus();
    expect(screen.getByRole("tooltip")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });
});
