import { expect, type Page, test } from "@playwright/test";

import { signIn } from "../support/console";
import { seededProject } from "../support/instance";

/**
 * The console's typefaces are served by the binary: an instance may have
 * no route to a font host, so no screen may ask another origin for a font
 * or for anything else.
 */

/** A screen of the console, signed in or not. */
const screens = ["/sign-in", "/admin/projects", `/projects/${seededProject.key}/flags`];

/** Records every request the page makes to an origin other than its own. */
function watchForeignRequests(page: Page, origin: string): string[] {
  const foreign: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.protocol.startsWith("http") && url.origin !== origin) foreign.push(request.url());
  });
  return foreign;
}

test.describe("the bundled typeface", () => {
  test("is requested from the instance itself on every screen", async ({ page, baseURL }) => {
    const origin = new URL(baseURL ?? "").origin;
    const foreign = watchForeignRequests(page, origin);
    const fonts: string[] = [];
    page.on("response", (response) => {
      if (response.request().resourceType() === "font") fonts.push(response.url());
    });

    await page.goto(screens[0] ?? "/");
    await signIn(page);
    for (const screen of screens.slice(1)) {
      await page.goto(screen);
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    }

    expect(foreign).toEqual([]);
    expect(fonts.length).toBeGreaterThan(0);
    for (const font of fonts) expect(new URL(font).origin).toBe(origin);
    expect(await page.evaluate(() => document.fonts.check("14px 'Geist Variable'"))).toBe(true);
  });

  test("ships its licence with the binary", async ({ request }) => {
    const response = await request.get("/third-party-notices.txt");

    expect(response.ok()).toBe(true);
    expect(await response.text()).toContain("SIL OPEN FONT LICENSE Version 1.1");
  });
});
