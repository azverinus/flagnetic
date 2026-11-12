import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { ExportProject } from "./export-project";

beforeEach(() => {
  activateForTests("en");
  URL.createObjectURL = vi.fn(() => "blob:document");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => vi.restoreAllMocks());

describe("exporting a project", () => {
  it("asks for JSON by default and saves the file under the server's name", async () => {
    const server = fakeServer({
      "GET /projects/checkout/export": {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Content-Disposition": 'attachment; filename="checkout-20261112T101500Z.json"',
        },
      },
    });
    const clicked: { download: string; href: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicked.push({ download: this.download, href: this.href });
    });
    renderWithProviders(<ExportProject project="checkout" />);

    await userEvent.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() =>
      expect(clicked).toEqual([
        { download: "checkout-20261112T101500Z.json", href: "blob:document" },
      ]),
    );
    expect(new URL(server.requests[0]!.url).searchParams.get("format")).toBe("json");
  });

  it("asks for YAML once chosen", async () => {
    const server = fakeServer({
      "GET /projects/checkout/export": {
        status: 200,
        headers: { "Content-Type": "application/yaml" },
      },
    });
    const clicked: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicked.push(this.download);
    });
    renderWithProviders(<ExportProject project="checkout" />);

    await userEvent.click(screen.getByRole("combobox", { name: "Document format" }));
    await userEvent.click(await screen.findByRole("option", { name: "YAML" }));
    await userEvent.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() => expect(clicked).toEqual(["checkout.yaml"]));
    expect(new URL(server.requests[0]!.url).searchParams.get("format")).toBe("yaml");
  });

  it("saves nothing when the server refuses", async () => {
    fakeServer({ "GET /projects/checkout/export": problem(404, "not_found") });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    renderWithProviders(<ExportProject project="checkout" />);

    await userEvent.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() => expect(screen.getByRole("button", { name: "Export" })).toBeEnabled());
    expect(click).not.toHaveBeenCalled();
  });
});
