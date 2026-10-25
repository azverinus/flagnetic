import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/errors";
import { renderWithProviders } from "@/test/render";

import { ConfirmDialog } from "./confirm-dialog";

describe("a confirmation", () => {
  it("names the consequence and confirms once asked", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    renderWithProviders(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title="Delete the environment staging?"
        confirmLabel="Delete"
        destructive
        onConfirm={onConfirm}
      >
        Its configuration in every flag is deleted with it.
      </ConfirmDialog>,
    );

    expect(
      screen.getByRole("dialog", { name: "Delete the environment staging?" }),
    ).toHaveTextContent("Its configuration in every flag is deleted with it.");
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("offers the action only once the key is typed out", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title="Delete the project?"
        confirmLabel="Delete"
        typeToConfirm="checkout"
        onConfirm={vi.fn()}
      >
        Everything in it is deleted.
      </ConfirmDialog>,
    );

    const confirm = screen.getByRole("button", { name: "Delete" });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText("Type checkout to confirm"), "checkou");
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText("Type checkout to confirm"), "t");
    expect(confirm).toBeEnabled();
  });

  it("cancels, and shows a refusal inside itself", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    renderWithProviders(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Delete?"
        confirmLabel="Delete"
        refusal={new ApiError(409, { code: "last_environment", detail: "The last one stays." })}
        onConfirm={vi.fn()}
      >
        Gone for good.
      </ConfirmDialog>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "This is the project's last environment. A project always keeps one to serve from.",
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("explains a refusal in the words its caller gives", () => {
    renderWithProviders(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title="Archive?"
        confirmLabel="Archive"
        refusal={new ApiError(409, { code: "archived" })}
        refusalMessage={(_, error) => `Refused: ${error.code}`}
        onConfirm={vi.fn()}
      >
        Nothing changes any more.
      </ConfirmDialog>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("Refused: archived");
  });
});
