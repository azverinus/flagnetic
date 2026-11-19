import type { Page } from "@playwright/test";

/**
 * Layout checks of the accessibility sweep, run in the page: nothing scrolls
 * sideways, no control sits under another, and a wide screen's content stops
 * at the page's cap.
 */

/** How far the page is wider than the window, in pixels; zero or less when it is not. */
export async function sidewaysOverflow(page: Page): Promise<number> {
  return page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
}

/**
 * The controls that cannot be reached at their centre: each interactive
 * control is scrolled into view and the element found at its centre must be
 * the control, or sit inside it or around it (a label that wraps its input).
 * Anything else, a sibling's box or a header floating over it, means two
 * controls overlap. One line each, naming the control and what covers it.
 */
export async function overlappingControls(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const selector = [
      "a[href]",
      "button",
      "input:not([type=hidden])",
      "select",
      "textarea",
      "summary",
      '[role="button"]',
      '[role="link"]',
      '[role="checkbox"]',
      '[role="combobox"]',
      '[role="menuitem"]',
      '[role="radio"]',
      '[role="switch"]',
      '[role="tab"]',
      '[tabindex]:not([tabindex="-1"])',
    ].join(",");

    const describe = (el: Element): string => {
      const label =
        el.getAttribute("aria-label") ??
        (el.textContent ?? "").trim().replace(/\s+/g, " ").slice(0, 40) ??
        "";
      return `${el.tagName.toLowerCase()}${label ? ` "${label}"` : ""}`;
    };

    const failures: string[] = [];
    for (const el of document.querySelectorAll(selector)) {
      if (!(el as HTMLElement).checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) {
        continue;
      }
      // A control that is hidden visually (the skip link, a screen-reader-only
      // input) is not on the page to be hit.
      const box = el.getBoundingClientRect();
      if (box.width <= 2 || box.height <= 2) continue;
      // Clipped away the way "sr-only" does, whatever padding it keeps.
      const style = getComputedStyle(el);
      if (style.clipPath !== "none" || (style.position === "absolute" && style.width === "1px")) {
        continue;
      }
      // A closed drawer or sheet keeps its controls off screen.
      if (el.closest('[inert], [aria-hidden="true"]')) continue;
      el.scrollIntoView({ block: "center", inline: "center" });
      const at = el.getBoundingClientRect();
      const x = at.left + at.width / 2;
      const y = at.top + at.height / 2;
      if (x < 0 || y < 0 || x >= window.innerWidth || y >= window.innerHeight) continue;
      const hit = document.elementFromPoint(x, y);
      if (!hit || hit === el || el.contains(hit) || hit.contains(el)) continue;
      failures.push(`${describe(el)} is covered by ${describe(hit)}`);
    }
    return failures;
  });
}

/** The widest a screen's content is, in pixels: the page's own container, which caps at 1600 px. */
export async function contentWidth(page: Page): Promise<number> {
  return page.evaluate(() =>
    Math.max(
      0,
      ...[...document.querySelectorAll("[data-measure]")].map(
        (el) => el.getBoundingClientRect().width,
      ),
    ),
  );
}
