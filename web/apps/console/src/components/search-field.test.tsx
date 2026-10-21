import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/test/render";

import { SearchField } from "./search-field";

/** The field under an address it reads its search from and writes it to. */
function Addressed({ onApplied }: { onApplied: (q: string) => void }) {
  const [q, setQ] = useState("ada");
  return (
    <>
      <SearchField
        value={q}
        onChange={(next) => {
          onApplied(next);
          setQ(next.trim());
        }}
      />
      <button type="button" onClick={() => setQ("")}>
        Clear the address
      </button>
    </>
  );
}

afterEach(() => {
  vi.useRealTimers();
});

describe("the search field", () => {
  it("applies what is typed once typing pauses", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const applied = vi.fn();
    renderWithProviders(<Addressed onApplied={applied} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });

    await user.type(screen.getByLabelText("Search"), " lovelace");
    expect(applied).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(300));

    expect(applied).toHaveBeenCalledTimes(1);
    expect(applied).toHaveBeenCalledWith("ada lovelace");
  });

  it("follows a search the address drops, without writing the old one back", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const applied = vi.fn();
    renderWithProviders(<Addressed onApplied={applied} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    expect(screen.getByLabelText("Search")).toHaveValue("ada");

    await user.click(screen.getByRole("button", { name: "Clear the address" }));
    await act(() => vi.advanceTimersByTimeAsync(300));

    expect(screen.getByLabelText("Search")).toHaveValue("");
    expect(applied).not.toHaveBeenCalled();
  });
});
