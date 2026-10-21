import { describe, expect, it, vi } from "vitest";

import { SignInForm } from "@/components/sign-in-form";
import { fakeServer } from "@/test/fake-server";
import { activateForTests, renderWithProviders } from "@/test/render";

/**
 * Every text node and every accessible name of what renders, which in the
 * pseudo-locale must all come from a catalog.
 */
function visibleStrings(root: HTMLElement): string[] {
  const out: string[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent?.trim();
    if (text) out.push(text);
  }
  for (const element of root.querySelectorAll("[aria-label], [title], [placeholder]")) {
    for (const name of ["aria-label", "title", "placeholder"]) {
      const value = element.getAttribute(name)?.trim();
      if (value) out.push(value);
    }
  }
  return out;
}

/** A pseudo-localized message: wrapped in the markers the catalog adds. */
const localized = /^⟦[\s\S]*⟧$/;

describe("the pseudo-locale", () => {
  it("renders every string of the sign-in form from the catalog", () => {
    fakeServer({});
    activateForTests("pseudo");
    const { container } = renderWithProviders(<SignInForm ended onSignedIn={vi.fn()} />);

    const strings = visibleStrings(container);

    expect(strings.length).toBeGreaterThan(3);
    expect(strings.filter((s) => !localized.test(s))).toEqual([]);
  });
});
