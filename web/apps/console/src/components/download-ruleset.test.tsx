import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fakeServer, problem } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

import { DownloadRuleset } from "./download-ruleset";

/** jsdom's Blob cannot be read with text(). */
function textOf(blob: Blob): Promise<string> {
  return new Promise((done) => {
    const reader = new FileReader();
    reader.onload = () => done(String(reader.result));
    reader.readAsText(blob);
  });
}

const path = "GET /projects/checkout/environments/staging/ruleset";

beforeEach(() => {
  activateForTests("en");
  URL.createObjectURL = vi.fn(() => "blob:ruleset");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => vi.restoreAllMocks());

describe("downloading an environment's ruleset", () => {
  it("saves the document as a file named after the project, environment and version", async () => {
    const document = { schema_version: 1, version: 42, flags: [] };
    fakeServer({ [path]: { status: 200, body: document } });
    const clicked: { download: string; href: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicked.push({ download: this.download, href: this.href });
    });
    renderWithProviders(<DownloadRuleset project="checkout" environment="staging" />);

    await userEvent.click(screen.getByRole("button", { name: "Download ruleset" }));

    await waitFor(() =>
      expect(clicked).toEqual([
        { download: "checkout-staging-ruleset-42.json", href: "blob:ruleset" },
      ]),
    );
    const blob = vi.mocked(URL.createObjectURL).mock.calls[0]?.[0] as Blob;
    expect(JSON.parse(await textOf(blob))).toEqual(document);
  });

  it("saves nothing when the server cannot load the ruleset", async () => {
    fakeServer({ [path]: problem(503, "ruleset_unavailable") });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    const { queryClient } = renderWithProviders(
      <DownloadRuleset project="checkout" environment="staging" />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Download ruleset" }));

    await waitFor(() =>
      expect(queryClient.getMutationCache().getAll()[0]?.state.status).toBe("error"),
    );
    expect(click).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Download ruleset" })).toBeEnabled();
  });
});
