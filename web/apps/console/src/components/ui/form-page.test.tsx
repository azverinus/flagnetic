import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/errors";
import { renderWithProviders } from "@/test/render";

import { Field } from "./field";
import { FormPage } from "./form-page";
import { Input } from "./input";

describe("a form page", () => {
  it("puts cancel before the primary action, which is rightmost", () => {
    renderWithProviders(
      <FormPage onSubmit={vi.fn()} submitLabel="Create project" onCancel={vi.fn()}>
        <Field label="Name">
          <Input />
        </Field>
      </FormPage>,
    );

    const buttons = screen.getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual(["Cancel", "Create project"]);
    expect(buttons[1]).toHaveAttribute("type", "submit");
    expect(buttons[0]).toHaveAttribute("type", "button");
    expect(buttons[0]!.parentElement).toHaveClass("justify-end");
  });

  it("submits with Enter from a field, and cancels without submitting", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn((event: { preventDefault: () => void }) => event.preventDefault());
    const onCancel = vi.fn();
    renderWithProviders(
      <FormPage onSubmit={onSubmit} submitLabel="Save" onCancel={onCancel}>
        <Field label="Name">
          <Input />
        </Field>
      </FormPage>,
    );

    await user.type(screen.getByLabelText("Name"), "Checkout{Enter}");
    expect(onSubmit).toHaveBeenCalledOnce();

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
    expect(onSubmit).toHaveBeenCalledOnce();
  });

  it("explains a refusal above the actions and holds the primary action while pending", () => {
    renderWithProviders(
      <FormPage
        onSubmit={vi.fn()}
        submitLabel="Save"
        pending
        refusal={new ApiError(409, { code: "already_exists" })}
      >
        <p>fields</p>
      </FormPage>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("One with this key already exists.");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
});
