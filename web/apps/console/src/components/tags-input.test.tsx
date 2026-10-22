import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { TagsInput } from "./tags-input";

beforeAll(() => activateForTests("en"));

function Form({ onSubmit }: { onSubmit: () => void }) {
  const [tags, setTags] = useState<string[]>([]);
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSubmit();
      }}
    >
      <label htmlFor="tags">Tags</label>
      <TagsInput id="tags" value={tags} onChange={setTags} />
      <button type="submit">Create flag</button>
    </form>
  );
}

describe("a tags input in a form", () => {
  it("adds each tag with Enter without submitting the form", async () => {
    const submitted = vi.fn();
    renderWithProviders(<Form onSubmit={submitted} />);
    await userEvent.type(screen.getByLabelText("Tags"), "search{Enter}web{Enter}");
    expect(screen.getByRole("button", { name: "Remove tag search" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove tag web" })).toBeInTheDocument();
    expect(submitted).not.toHaveBeenCalled();
  });

  it("does not submit the form on Enter once a tag typed was added on leaving the field", async () => {
    const submitted = vi.fn();
    renderWithProviders(<Form onSubmit={submitted} />);
    const tags = screen.getByLabelText("Tags");
    await userEvent.type(tags, "search");
    await userEvent.tab();
    await userEvent.type(tags, "{Enter}");
    expect(screen.getByRole("button", { name: "Remove tag search" })).toBeInTheDocument();
    expect(submitted).not.toHaveBeenCalled();
  });
});
