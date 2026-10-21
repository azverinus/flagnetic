import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

/**
 * Every routed screen is checked in both themes against WCAG 2.1 A and AA.
 * A serious or critical violation fails the suite.
 */
export async function expectNoSeriousViolations(page: Page, context: string): Promise<void> {
  expect(
    await seriousViolations(page),
    `serious or critical accessibility violations on ${context}`,
  ).toEqual([]);
}

/** The page's serious and critical violations, one line each with where the first one is. */
export async function seriousViolations(page: Page): Promise<string[]> {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  return results.violations
    .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    .map(
      (violation) =>
        `${violation.id}: ${violation.help} (${violation.nodes.length} nodes, first ${violation.nodes[0]?.target.join(" ")})`,
    );
}

/** Puts the page in a theme, as the personal menu does, and reloads into it. */
export async function useTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.addInitScript((value) => {
    window.localStorage.setItem("flagnetic.theme", value);
  }, theme);
}
