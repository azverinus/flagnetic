import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/errors";
import { renderWithProviders } from "@/test/render";

import { Field } from "./field";
import { Input } from "./input";
import { SwitchField } from "./switch-field";

describe("a field", () => {
  it("names its control by the label above it and reads the hint with it", () => {
    renderWithProviders(
      <Field label="Key" hint="Lowercase letters and digits.">
        <Input />
      </Field>,
    );

    const input = screen.getByRole("textbox", { name: "Key" });
    expect(input).toHaveAccessibleDescription("Lowercase letters and digits.");
    expect(input).not.toHaveAttribute("aria-invalid");
  });

  it("keeps the control's own id", () => {
    renderWithProviders(
      <Field label="Name">
        <Input id="project-name" />
      </Field>,
    );

    expect(screen.getByLabelText("Name")).toHaveAttribute("id", "project-name");
  });

  it("shows the server's violation for its JSON Pointer, marking the control invalid", () => {
    const refusal = new ApiError(422, {
      code: "invalid_project",
      errors: [
        { pointer: "/name", code: "too_long", detail: "too long", params: { max: 200 } },
        { pointer: "/key", code: "invalid_key", detail: "bad" },
      ],
    });
    renderWithProviders(
      <Field label="Name" hint="Shown everywhere." refusal={refusal} pointer="/name">
        <Input />
      </Field>,
    );

    const input = screen.getByRole("textbox", { name: "Name" });
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Shown everywhere. At most 200 characters.");
  });

  it("shows the form's own error before the server's", () => {
    renderWithProviders(
      <Field label="Name" error="Required.">
        <Input />
      </Field>,
    );

    expect(screen.getByRole("textbox", { name: "Name" })).toHaveAccessibleDescription("Required.");
  });

  it("shows nothing for a violation of another field", () => {
    const refusal = new ApiError(422, {
      code: "invalid_project",
      errors: [{ pointer: "/key", code: "invalid_key", detail: "bad" }],
    });
    renderWithProviders(
      <Field label="Name" refusal={refusal} pointer="/name">
        <Input />
      </Field>,
    );

    expect(screen.getByRole("textbox", { name: "Name" })).not.toHaveAttribute("aria-invalid");
  });
});

describe("a field's details", () => {
  it("are closed until opened, then read as an expanded disclosure", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <Field label="Key" hint="Lowercase letters and digits." details="Run the unlock command.">
        <Input />
      </Field>,
    );

    const summary = screen.getByText("Details");
    const disclosure = summary.closest("details");
    expect(disclosure).not.toHaveAttribute("open");

    await user.click(summary);

    expect(disclosure).toHaveAttribute("open");
    expect(screen.getByText("Run the unlock command.")).toBeVisible();
  });

  it("do not appear without any", () => {
    renderWithProviders(
      <Field label="Key">
        <Input />
      </Field>,
    );

    expect(screen.queryByText("Details")).not.toBeInTheDocument();
  });

  it("are offered by a switch field as well", () => {
    renderWithProviders(
      <SwitchField
        label="Enabled"
        hint="Offer it now."
        details="Turn it off to test first."
        checked
        onCheckedChange={() => {}}
      />,
    );

    expect(screen.getByText("Details").closest("details")).not.toHaveAttribute("open");
    expect(screen.getByRole("switch", { name: "Enabled" })).toHaveAccessibleDescription(
      "Offer it now.",
    );
  });
});
