import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { activateForTests, renderWithProviders } from "@/test/render";

import { PanelChecklist, PanelFacet, PanelTextFacet } from "./panel-facet";

beforeEach(() => activateForTests("en"));

const options = (count: number) =>
  Array.from({ length: count }, (_, i) => ({ value: `v${i}`, label: `Option ${i}` }));

describe("PanelFacet", () => {
  it("is a radio list up to five options, the facet unset as its first choice", () => {
    renderWithProviders(
      <PanelFacet
        label="Type"
        value={undefined}
        onChange={() => {}}
        options={options(5)}
        anyLabel="Any type"
      />,
    );

    const group = screen.getByRole("radiogroup", { name: "Type" });
    expect(group).toBeInTheDocument();
    const radios = screen.getAllByRole("radio");
    expect(radios.map((radio) => radio.parentElement?.textContent)).toEqual([
      "Any type",
      ...options(5).map((option) => option.label),
    ]);
    expect(screen.getByRole("radio", { name: "Any type" })).toBeChecked();
  });

  it("applies a choice at once, and clears with the unset choice", async () => {
    const user = userEvent.setup();
    const changed = vi.fn();
    function Facet() {
      const [value, setValue] = useState<string | undefined>(undefined);
      return (
        <PanelFacet
          label="Type"
          value={value}
          onChange={(next) => {
            changed(next);
            setValue(next);
          }}
          options={options(3)}
          anyLabel="Any type"
        />
      );
    }
    renderWithProviders(<Facet />);

    await user.click(screen.getByRole("radio", { name: "Option 1" }));
    expect(changed).toHaveBeenLastCalledWith("v1");
    expect(screen.getByRole("radio", { name: "Option 1" })).toBeChecked();

    await user.click(screen.getByRole("radio", { name: "Any type" }));
    expect(changed).toHaveBeenLastCalledWith(undefined);
  });

  it("is a select beyond five options, applying at once", async () => {
    const user = userEvent.setup();
    const changed = vi.fn();
    renderWithProviders(
      <PanelFacet
        label="Owner"
        value={undefined}
        onChange={changed}
        options={options(6)}
        anyLabel="Any owner"
      />,
    );

    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    await user.click(screen.getByRole("combobox", { name: "Owner" }));
    await user.click(screen.getByRole("option", { name: "Option 4" }));
    expect(changed).toHaveBeenLastCalledWith("v4");
  });
});

describe("PanelChecklist", () => {
  it("applies each box at once, keeping the others", async () => {
    const user = userEvent.setup();
    const changed = vi.fn();
    renderWithProviders(
      <PanelChecklist label="Stale for" values={["v0"]} onChange={changed} options={options(3)} />,
    );

    expect(screen.getByRole("checkbox", { name: "Option 0" })).toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "Option 2" }));
    expect(changed).toHaveBeenLastCalledWith(["v0", "v2"]);
    await user.click(screen.getByRole("checkbox", { name: "Option 0" }));
    expect(changed).toHaveBeenLastCalledWith([]);
  });
});

describe("PanelTextFacet", () => {
  it("applies what is typed once typing pauses", async () => {
    const user = userEvent.setup();
    const changed = vi.fn();
    renderWithProviders(<PanelTextFacet label="Tag" value="" onChange={changed} />);

    await user.type(screen.getByRole("searchbox", { name: "Tag" }), "web");
    await vi.waitFor(() => expect(changed).toHaveBeenLastCalledWith("web"));
  });
});
