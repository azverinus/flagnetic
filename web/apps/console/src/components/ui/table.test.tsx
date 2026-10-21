import type { ColumnDef } from "@tanstack/react-table";
import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/test/render";

import { Table } from "./table";

type Row = { key: string; name: string };
const columns: ColumnDef<Row, unknown>[] = [
  { accessorKey: "key", header: "Key" },
  { accessorKey: "name", header: "Name" },
];
const rows: Row[] = [
  { key: "checkout", name: "Checkout" },
  { key: "billing", name: "Billing" },
];

describe("a table", () => {
  it("is named by its caption, with column headers and one row per item", () => {
    renderWithProviders(<Table caption="Projects" columns={columns} data={rows} />);

    const table = screen.getByRole("table", { name: "Projects" });
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent),
    ).toEqual(["Key", "Name"]);
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(within(table).getByRole("cell", { name: "Billing" })).toBeInTheDocument();
  });

  it("scrolls inside its own positioned wrapper, so it cannot widen the page", () => {
    renderWithProviders(<Table caption="Projects" columns={columns} data={rows} />);

    const wrapper = screen.getByRole("table").parentElement!;
    expect(wrapper).toHaveClass("relative", "overflow-x-auto", "max-w-full");
  });

  it("keeps the first column in view when asked", () => {
    renderWithProviders(
      <Table caption="Projects" columns={columns} data={rows} stickyFirstColumn />,
    );

    expect(screen.getByRole("columnheader", { name: "Key" })).toHaveClass("sticky", "left-0");
    expect(screen.getByRole("cell", { name: "checkout" })).toHaveClass("sticky");
    expect(screen.getByRole("cell", { name: "Checkout" })).not.toHaveClass("sticky");
  });

  it("says what would be here when there are no rows", () => {
    renderWithProviders(
      <Table caption="Projects" columns={columns} data={[]} empty="No projects yet." />,
    );

    const cell = screen.getByRole("cell", { name: "No projects yet." });
    expect(cell).toHaveAttribute("colspan", "2");
  });

  it("is busy while loading, and says so to a screen reader", () => {
    renderWithProviders(<Table caption="Projects" columns={columns} data={[]} loading />);

    expect(screen.getByRole("table")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByText("Nothing here yet.")).not.toBeInTheDocument();
  });

  it("can hide its caption from sight but not from a screen reader", () => {
    renderWithProviders(<Table caption="Projects" hideCaption columns={columns} data={rows} />);

    expect(screen.getByText("Projects")).toHaveClass("sr-only");
    expect(screen.getByRole("table", { name: "Projects" })).toBeInTheDocument();
  });
});
