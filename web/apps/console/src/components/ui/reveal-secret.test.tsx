import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/test/render";

import { RevealSecretDialog } from "./reveal-secret";

const value = "fn_pat_0123456789abcdefghijABCDEFGHIJ0123456789";

describe("the one-time reveal of a secret", () => {
  it("cannot be closed until the member says it is stored", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    renderWithProviders(
      <RevealSecretDialog
        open
        title="Your new token"
        kept="hash"
        value={value}
        onClose={onClose}
      />,
    );

    expect(screen.getByRole("dialog", { name: "Your new token" })).toHaveTextContent(value);
    const close = screen.getByRole("button", { name: "Close" });
    expect(close).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();

    await user.click(screen.getByLabelText("I have stored it somewhere safe"));
    await user.click(close);
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("copies the value and says so", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <RevealSecretDialog open title="Token" kept="hash" value={value} onClose={vi.fn()} />,
    );
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();

    await user.click(screen.getByRole("button", { name: "Copy" }));

    expect(writeText).toHaveBeenCalledWith(value);
    expect(await screen.findByRole("status")).toHaveTextContent("Copied to the clipboard.");
  });

  it("tells the member to copy by hand when the browser refuses the clipboard", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <RevealSecretDialog open title="Token" kept="hash" value={value} onClose={vi.fn()} />,
    );
    vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));

    await user.click(screen.getByRole("button", { name: "Copy" }));

    expect(await screen.findByRole("status")).toHaveTextContent(
      "The browser refused the clipboard.",
    );
  });

  it("says what the member does on losing it, by how the instance keeps it", () => {
    const { unmount } = renderWithProviders(
      <RevealSecretDialog open title="Token" kept="hash" value={value} onClose={vi.fn()} />,
    );
    expect(screen.getByRole("dialog")).toHaveTextContent(
      "Only a hash of it is kept, so if it is lost, replace it.",
    );
    unmount();

    renderWithProviders(
      <RevealSecretDialog
        open
        title="Signing secret"
        kept="sealed"
        value={value}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByRole("dialog")).toHaveTextContent(
      "It is kept encrypted and never shown again, so if it is lost, rotate it.",
    );
    expect(screen.getByRole("dialog")).not.toHaveTextContent("hash");
  });
});
