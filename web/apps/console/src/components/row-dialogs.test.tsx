import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Trash2Icon } from "lucide-react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { RowAction } from "./row-dialogs";

beforeAll(() => activateForTests("en"));

describe("a row's action", () => {
  it("acts when clicked", async () => {
    const onClick = vi.fn();
    renderWithProviders(<RowAction label="Delete staging" icon={Trash2Icon} onClick={onClick} />);

    await userEvent.click(screen.getByRole("button", { name: "Delete staging" }));

    expect(onClick).toHaveBeenCalledOnce();
  });

  it("is shown off and does nothing where the server always refuses it", async () => {
    const onClick = vi.fn();
    renderWithProviders(
      <RowAction
        label="Delete production"
        icon={Trash2Icon}
        onClick={onClick}
        refused="Stop protecting it first."
      />,
    );

    const button = screen.getByRole("button", { name: "Delete production" });
    expect(button).toHaveAttribute("aria-disabled", "true");
    await userEvent.click(button, { pointerEventsCheck: 0 });
    expect(onClick).not.toHaveBeenCalled();
  });
});
