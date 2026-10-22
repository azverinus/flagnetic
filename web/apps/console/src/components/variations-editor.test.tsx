import type { FlagType } from "@flagnetic/api";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/errors";
import { startingVariations, type VariationDraft } from "@/lib/variations";
import { activateForTests, renderWithProviders } from "@/test/render";

import { VariationsEditor, type VariationsEditorProps } from "./variations-editor";

beforeAll(() => activateForTests("en"));

/** Every change the editor made, the rows it changed them to. */
const changes = vi.fn<(rows: VariationDraft[]) => void>();
const latest = () => changes.mock.lastCall?.[0] ?? [];

beforeEach(() => changes.mockClear());

function Editor({
  type,
  errors,
  refusal,
}: { type: FlagType } & Pick<VariationsEditorProps, "errors" | "refusal">) {
  const [rows, setRows] = useState(startingVariations);
  return (
    <VariationsEditor
      type={type}
      rows={rows}
      onChange={(next) => {
        setRows(next);
        changes(next);
      }}
      errors={errors}
      refusal={refusal}
    />
  );
}

const variation = (n: number) => screen.getByRole("group", { name: `Variation ${n}` });

describe("the variations editor", () => {
  it("shows a boolean flag's two fixed variations and nothing to edit", () => {
    renderWithProviders(<Editor type="boolean" />);

    const fixed = screen.getByRole("list", { name: "Variations" });
    expect(
      within(fixed)
        .getAllByRole("listitem")
        .map((item) => item.textContent),
    ).toEqual(["true", "false"]);
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "Add variation" })).toBeNull();
  });

  it("starts any other flag with two rows to fill in", () => {
    renderWithProviders(<Editor type="string" />);

    expect(within(variation(1)).getByLabelText("Key")).toHaveValue("v1");
    expect(within(variation(2)).getByLabelText("Key")).toHaveValue("v2");
    expect(screen.queryByRole("group", { name: "Variation 3" })).toBeNull();
  });

  it("edits a row's fields in place", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Editor type="string" />);

    const second = within(variation(2));
    await user.clear(second.getByLabelText("Key"));
    await user.type(second.getByLabelText("Key"), "blue");
    await user.type(second.getByLabelText("Value"), "Blue");
    await user.type(second.getByLabelText("Name"), "Blue sky");
    await user.type(second.getByLabelText("Description"), "Cooler");

    expect(latest()[1]).toMatchObject({
      key: "blue",
      value: "Blue",
      name: "Blue sky",
      description: "Cooler",
    });
    expect(latest()[0]).toMatchObject({ key: "v1", value: "" });
  });

  it("adds rows and removes any but the last one left", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Editor type="number" />);

    await user.click(screen.getByRole("button", { name: "Add variation" }));
    expect(within(variation(3)).getByLabelText("Key")).toHaveValue("v3");

    await user.click(screen.getByRole("button", { name: "Remove variation 1" }));
    await user.click(screen.getByRole("button", { name: "Remove variation 1" }));
    expect(latest().map((row) => row.key)).toEqual(["v3"]);
    expect(screen.getByRole("button", { name: "Remove variation 1" })).toBeDisabled();
  });

  it("takes a JSON value on several lines", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Editor type="json" />);

    const value = within(variation(1)).getByLabelText("Value");
    expect(value.tagName).toBe("TEXTAREA");
    await user.click(value);
    await user.paste('{\n  "limit": 3\n}');
    expect(latest()[0]?.value).toBe('{\n  "limit": 3\n}');
  });

  it("shows the form's error under the field it concerns", () => {
    renderWithProviders(
      <Editor type="number" errors={[undefined, { value: "Enter a number." }]} />,
    );

    expect(within(variation(2)).getByLabelText("Value")).toHaveAccessibleDescription(
      "Enter a number.",
    );
    expect(within(variation(1)).getByLabelText("Value")).not.toHaveAccessibleDescription(
      "Enter a number.",
    );
  });

  it("shows the server's refusal of a row under the field it points at", () => {
    const refusal = new ApiError(422, {
      code: "invalid_flag",
      errors: [
        { pointer: "/variations/0/value", code: "invalid_type", detail: "" },
        { pointer: "/variations/1/key", code: "invalid_key", detail: "" },
      ],
    });
    renderWithProviders(<Editor type="string" refusal={refusal} />);

    expect(within(variation(1)).getByLabelText("Value")).toHaveAccessibleDescription(
      "Enter a value of the flag's type.",
    );
    expect(within(variation(2)).getByLabelText("Key")).toHaveAccessibleDescription(
      "Letters, digits, ., - and _, at most 64.",
    );
  });
});
