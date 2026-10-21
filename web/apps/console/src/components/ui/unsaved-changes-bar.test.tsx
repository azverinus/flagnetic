import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { renderWithRouter } from "@/test/render";

import { UnsavedChangesBar } from "./unsaved-changes-bar";

function Form({ onSave = vi.fn() }: { onSave?: () => void }) {
  const [dirty, setDirty] = useState(false);
  return (
    <main className="relative">
      <button onClick={() => setDirty(true)}>Edit</button>
      <UnsavedChangesBar dirty={dirty} onDiscard={() => setDirty(false)} onSave={onSave} />
    </main>
  );
}

describe("the unsaved-changes bar", () => {
  it("shows only while the form holds changes, as a named region", async () => {
    const user = userEvent.setup();
    await renderWithRouter(() => <Form />);

    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toHaveTextContent(
      "You have unsaved changes.",
    );

    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  });

  it("sticks to the bottom of the main column rather than the window", async () => {
    const user = userEvent.setup();
    await renderWithRouter(() => <Form />);
    await user.click(screen.getByRole("button", { name: "Edit" }));

    const bar = screen.getByRole("region", { name: "Unsaved changes" });
    expect(bar).toHaveClass("sticky", "bottom-0");
    expect(bar).not.toHaveClass("fixed");
  });

  it("lifts toasts above itself while it shows", async () => {
    const user = userEvent.setup();
    await renderWithRouter(() => <Form />);

    await user.click(screen.getByRole("button", { name: "Edit" }));
    expect(document.documentElement.style.getPropertyValue("--toast-offset")).toMatch(/px$/);

    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(document.documentElement.style.getPropertyValue("--toast-offset")).toBe("");
  });

  it("saves through the form", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    await renderWithRouter(() => <Form onSave={onSave} />);

    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledOnce();
  });

  it("asks before leaving the page, staying when the member cancels", async () => {
    const user = userEvent.setup();
    const { router } = await renderWithRouter(() => <Form />);
    await user.click(screen.getByRole("button", { name: "Edit" }));

    act(() => router.history.push("/elsewhere"));
    const dialog = await screen.findByRole("dialog", { name: "Leave without saving?" });
    expect(dialog).toHaveTextContent("Your changes on this page will be lost.");

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(router.state.location.pathname).toBe("/");
  });

  it("leaves when the member confirms", async () => {
    const user = userEvent.setup();
    const { router } = await renderWithRouter(() => <Form />);
    await user.click(screen.getByRole("button", { name: "Edit" }));

    act(() => router.history.push("/elsewhere"));
    await user.click(await screen.findByRole("button", { name: "Leave" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/elsewhere"));
  });

  it("lets the member leave freely without changes", async () => {
    const { router } = await renderWithRouter(() => <Form />);

    act(() => router.history.push("/elsewhere"));

    await waitFor(() => expect(router.state.location.pathname).toBe("/elsewhere"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
