import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { DownloadAudit } from "./download-audit";

beforeEach(() => {
  activateForTests("en");
  URL.createObjectURL = vi.fn(() => "blob:audit");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => vi.restoreAllMocks());

function saved() {
  const clicked: { download: string; href: string }[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    clicked.push({ download: this.download, href: this.href });
  });
  return clicked;
}

describe("downloading the audit log", () => {
  it("asks for the format chosen with the filters applied and saves it under the server's name", async () => {
    const server = fakeServer({
      "GET /projects/checkout/audit/export": {
        status: 200,
        body: undefined,
        headers: {
          "Content-Type": "text/csv",
          "Content-Disposition": 'attachment; filename="audit-checkout-20261028T101500Z.csv"',
        },
      },
    });
    const clicked = saved();
    renderWithProviders(
      <DownloadAudit
        scope={{ project: "checkout" }}
        search={{ action: "flag.kill_switch", q: "pay", cursor: "next" }}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Download CSV" }));

    await waitFor(() =>
      expect(clicked).toEqual([
        { download: "audit-checkout-20261028T101500Z.csv", href: "blob:audit" },
      ]),
    );
    const url = new URL(server.requests[0]!.url);
    expect(url.searchParams.get("format")).toBe("csv");
    expect(url.searchParams.get("action")).toBe("flag.kill_switch");
    expect(url.searchParams.get("q")).toBe("pay");
    expect(url.searchParams.has("cursor")).toBe(false);
  });

  it("downloads the instance log narrowed to a project, as JSON Lines", async () => {
    const server = fakeServer({
      "GET /audit/export": { status: 200, headers: { "Content-Type": "application/x-ndjson" } },
    });
    const clicked = saved();
    renderWithProviders(<DownloadAudit scope="instance" search={{ project: "0192a0" }} />);

    await userEvent.click(screen.getByRole("button", { name: "Download JSON Lines" }));

    await waitFor(() => expect(clicked).toHaveLength(1));
    expect(clicked[0]?.download).toMatch(/^audit-instance-\d{8}T\d{6}Z\.jsonl$/);
    const url = new URL(server.requests[0]!.url);
    expect(url.searchParams.get("format")).toBe("jsonl");
    expect(url.searchParams.get("project")).toBe("0192a0");
  });

  it("saves nothing when the filters match more than the server will send", async () => {
    fakeServer({ "GET /projects/checkout/audit/export": problem(422, "export_too_large") });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const { queryClient } = renderWithProviders(
      <DownloadAudit scope={{ project: "checkout" }} search={{}} />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Download CSV" }));

    await waitFor(() =>
      expect(queryClient.getMutationCache().getAll()[0]?.state.error).toMatchObject({
        code: "export_too_large",
      }),
    );
    expect(click).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Download CSV" })).toBeEnabled();
  });
});
