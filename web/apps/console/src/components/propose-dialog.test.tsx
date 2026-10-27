import type { ChangeRequestCreate, Violation } from "@flagnetic/api";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithRouter } from "@/test/render";

import { type Proposal, ProposeDialog } from "./propose-dialog";

beforeAll(() => activateForTests("en"));

const proposal: Proposal = {
  kind: "flag_configuration",
  flag: "checkout_title",
  environment: "production",
  proposed: { on: true, off_variation: "a", fallthrough: "b" },
};

const created = { id: "0192a000-0000-7000-8000-0000000000aa", state: "open", version: 1 };

async function renderPropose(
  over: { before?: () => Promise<unknown>; onRefusedProposal?: (v: Violation[]) => void } = {},
) {
  const onProposed = vi.fn();
  const onOpenChange = vi.fn();
  const rendered = await renderWithRouter(() => (
    <ProposeDialog
      project="shop"
      proposal={proposal}
      onOpenChange={onOpenChange}
      onProposed={onProposed}
      before={over.before}
      onRefusedProposal={over.onRefusedProposal}
    />
  ));
  return {
    ...rendered,
    onProposed,
    onOpenChange,
    dialog: screen.getByRole("dialog", { name: "Propose the change" }),
  };
}

async function bodyOf(request: Request | undefined): Promise<ChangeRequestCreate> {
  return (await request!.json()) as ChangeRequestCreate;
}

describe("the propose dialog", () => {
  it("asks a title, then opens the change request with what is proposed", async () => {
    const { requests } = fakeServer({
      "POST /projects/shop/change-requests": { status: 201, body: created },
    });
    const { dialog, onProposed, router } = await renderPropose();

    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));
    expect(within(dialog).getByText("Enter a title.")).toBeVisible();
    expect(requests).toEqual([]);

    await userEvent.type(within(dialog).getByLabelText("Title"), "  Serve the new copy ");
    await userEvent.type(within(dialog).getByLabelText("Description"), "It tested well.");
    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));

    await waitFor(() => expect(onProposed).toHaveBeenCalledOnce());
    expect(await bodyOf(requests[0])).toEqual({
      ...proposal,
      title: "Serve the new copy",
      description: "It tested well.",
      open: true,
    });
    await waitFor(() =>
      expect(router.state.location.pathname).toBe(`/projects/shop/change-requests/${created.id}`),
    );
  });

  it("sends an apply time as the instant it names where the member is", async () => {
    const { requests } = fakeServer({
      "POST /projects/shop/change-requests": { status: 201, body: created },
    });
    const { dialog, onProposed } = await renderPropose();

    await userEvent.type(within(dialog).getByLabelText("Title"), "At night");
    await userEvent.type(within(dialog).getByLabelText("Apply at"), "2026-11-02T03:30");
    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));

    await waitFor(() => expect(onProposed).toHaveBeenCalledOnce());
    expect((await bodyOf(requests[0])).apply_at).toBe(new Date("2026-11-02T03:30").toISOString());
  });

  it("writes what the proposal needs first, once, and shows a refusal under its field", async () => {
    let answer = problem(422, "invalid_change_request", {
      errors: [{ pointer: "/apply_at", code: "too_soon" }],
    });
    const { requests } = fakeServer({
      "POST /projects/shop/change-requests": () => answer,
    });
    const before = vi.fn(() => Promise.resolve());
    const { dialog, onProposed } = await renderPropose({ before });

    await userEvent.type(within(dialog).getByLabelText("Title"), "Soon");
    await userEvent.type(within(dialog).getByLabelText("Apply at"), "2026-11-02T03:30");
    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));
    await waitFor(() =>
      expect(within(dialog).getByLabelText("Apply at")).toHaveAttribute("aria-invalid", "true"),
    );
    expect(before).toHaveBeenCalledOnce();
    expect(onProposed).not.toHaveBeenCalled();

    answer = { status: 201, body: created };
    await userEvent.clear(within(dialog).getByLabelText("Apply at"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));
    await waitFor(() => expect(onProposed).toHaveBeenCalledOnce());
    expect(before).toHaveBeenCalledOnce();
    expect(requests).toHaveLength(2);
    expect((await bodyOf(requests[1])).apply_at).toBeUndefined();
  });

  it("writes what a later proposal needs first, though an earlier one's write was done", async () => {
    let answer = problem(422, "invalid_change_request", {
      errors: [{ pointer: "/title", code: "required" }],
    });
    fakeServer({ "POST /projects/shop/change-requests": () => answer });
    const first = vi.fn(() => Promise.resolve());
    const second = vi.fn(() => Promise.resolve());
    const onProposed = vi.fn();
    function Editor() {
      const [shown, setShown] = useState<{ proposal: Proposal; before: () => Promise<unknown> }>({
        proposal,
        before: first,
      });
      return (
        <ProposeDialog
          project="shop"
          proposal={shown.proposal}
          onOpenChange={() => setShown({ proposal: { ...proposal }, before: second })}
          onProposed={onProposed}
          before={shown.before}
        />
      );
    }
    await renderWithRouter(() => <Editor />);

    let dialog = screen.getByRole("dialog", { name: "Propose the change" });
    await userEvent.type(within(dialog).getByLabelText("Title"), "First");
    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));
    await waitFor(() =>
      expect(within(dialog).getByLabelText("Title")).toHaveAttribute("aria-invalid", "true"),
    );
    expect(first).toHaveBeenCalledOnce();

    // The member leaves the dialog, edits again and the editor proposes anew.
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    answer = { status: 201, body: created };
    dialog = screen.getByRole("dialog", { name: "Propose the change" });
    await userEvent.type(within(dialog).getByLabelText("Title"), "Second");
    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));

    await waitFor(() => expect(onProposed).toHaveBeenCalledOnce());
    expect(second).toHaveBeenCalledOnce();
  });

  it("hands refusals of what is proposed back to the editor, as a direct write's", async () => {
    fakeServer({
      "POST /projects/shop/change-requests": problem(422, "invalid_configuration", {
        errors: [{ pointer: "/proposed/rules/0/conditions/0/values", code: "required" }],
      }),
    });
    const refused: Violation[][] = [];
    const { dialog, onOpenChange } = await renderPropose({
      onRefusedProposal: (violations) => refused.push(violations),
    });

    await userEvent.type(within(dialog).getByLabelText("Title"), "Beta");
    await userEvent.click(within(dialog).getByRole("button", { name: "Propose" }));

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(refused).toEqual([[{ pointer: "/rules/0/conditions/0/values", code: "required" }]]);
  });
});
