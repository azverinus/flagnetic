import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Button } from "./button";
import { Dialog } from "./dialog";
import { Sheet } from "./sheet";

function Opener({ dismissible = true, sheet = false }: { dismissible?: boolean; sheet?: boolean }) {
  const [open, setOpen] = useState(false);
  const body = (
    <>
      <input aria-label="Name" />
      <Button onClick={() => setOpen(false)}>Done</Button>
    </>
  );
  return (
    <>
      <Button onClick={() => setOpen(true)}>Open</Button>
      {sheet ? (
        <Sheet open={open} onOpenChange={setOpen} title="Edit environment">
          {body}
        </Sheet>
      ) : (
        <Dialog
          open={open}
          onOpenChange={setOpen}
          title="Rename project"
          description="Its key stays the same."
          dismissible={dismissible}
        >
          {body}
        </Dialog>
      )}
    </>
  );
}

describe("a dialog", () => {
  it("is named by its title and described by its description", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Opener />);

    await user.click(screen.getByRole("button", { name: "Open" }));

    const dialog = screen.getByRole("dialog", { name: "Rename project" });
    expect(dialog).toHaveAccessibleDescription("Its key stays the same.");
  });

  it("moves focus in, closes on Escape and gives focus back to its opener", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Opener />);
    const opener = screen.getByRole("button", { name: "Open" });

    await user.click(opener);
    expect(screen.getByRole("dialog")).toContainElement(document.activeElement as HTMLElement);

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(opener).toHaveFocus();
  });

  it("has a close button named for a screen reader", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Opener />);

    await user.click(screen.getByRole("button", { name: "Open" }));
    await user.click(screen.getByRole("button", { name: "Close" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("refuses Escape and offers no close button when it must be acted on", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Opener dismissible={false} />);

    await user.click(screen.getByRole("button", { name: "Open" }));
    await user.keyboard("{Escape}");

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
  });
});

describe("a sheet", () => {
  it("is a named modal panel that closes on Escape", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Opener sheet />);

    await user.click(screen.getByRole("button", { name: "Open" }));
    expect(screen.getByRole("dialog", { name: "Edit environment" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});
