import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { newAttributeCondition } from "@/lib/conditions";
import type { SegmentDraft } from "@/lib/segments";
import { activateForTests, renderWithProviders } from "@/test/render";

import { SegmentFields } from "./segment-fields";

beforeAll(() => activateForTests("en"));

const attributes = [{ key: "country", type: "string" as const, name: "Country", unitType: false }];

function Form({ onSubmit }: { onSubmit: () => void }) {
  const [draft, setDraft] = useState<SegmentDraft>({
    name: "EU",
    description: "",
    private: false,
    conditions: [{ ...newAttributeCondition("country", "string"), op: "in" }],
  });
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <SegmentFields draft={draft} onChange={setDraft} attributes={attributes} errors={new Map()} />
      <button type="submit">Create segment</button>
    </form>
  );
}

describe("a segment's fields in a form", () => {
  it("adds each value of a list with Enter without submitting the form", async () => {
    const submitted = vi.fn();
    renderWithProviders(<Form onSubmit={submitted} />);
    const value = screen.getByRole("textbox", { name: "Value (condition 1)" });
    await userEvent.type(value, "DE{Enter}FR{Enter}");
    expect(screen.getByRole("button", { name: "Remove DE" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove FR" })).toBeInTheDocument();
    expect(submitted).not.toHaveBeenCalled();
  });

  it("does not submit the form on Enter once a value typed was added on leaving the field", async () => {
    const submitted = vi.fn();
    renderWithProviders(<Form onSubmit={submitted} />);
    const value = screen.getByRole("textbox", { name: "Value (condition 1)" });
    await userEvent.type(value, "DE");
    await userEvent.tab();
    await userEvent.type(value, "{Enter}");
    expect(screen.getByRole("button", { name: "Remove DE" })).toBeInTheDocument();
    expect(submitted).not.toHaveBeenCalled();
  });
});
