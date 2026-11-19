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
        (el.textContent ?? "").trim().replace(/\s+/g, " ").slice(0, 40);
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
      // Clipped away the way "sr-only" does, whatever padding it keeps. Only
      // that clip: a control with another clip-path is on the page and checked.
      const style = getComputedStyle(el);
      if (
        style.clipPath.startsWith("inset(50%") ||
        style.clip === "rect(0px, 0px, 0px, 0px)" ||
        (style.position === "absolute" && style.width === "1px")
      ) {
        continue;
      }
      // A disabled control takes no pointer events, so the point at its centre
      // finds whatever is under it; it cannot be pressed, covered or not.
      if (el.matches(":disabled") || el.getAttribute("aria-disabled") === "true") continue;
      // A closed drawer or sheet keeps its controls off screen.
      if (el.closest('[inert], [aria-hidden="true"]')) continue;
      el.scrollIntoView({ block: "center", inline: "center" });
      // The part of the control a person can see: its box cut by the window
      // and by every ancestor that clips its overflow (a truncated link in a
      // table cell shows only its start). Its centre is where a press lands.
      // A link that wraps is several boxes, one per line, and the centre of
      // their bounding box can fall between them; its widest line is pressed.
      const at = [...el.getClientRects()].reduce((widest, rect) =>
        rect.width * rect.height > widest.width * widest.height ? rect : widest,
      );
      let left = Math.max(at.left, 0);
      let top = Math.max(at.top, 0);
      let right = Math.min(at.right, window.innerWidth);
      let bottom = Math.min(at.bottom, window.innerHeight);
      for (let up = el.parentElement; up; up = up.parentElement) {
        const { overflowX, overflowY } = getComputedStyle(up);
        if (overflowX === "visible" && overflowY === "visible") continue;
        const clip = up.getBoundingClientRect();
        left = Math.max(left, clip.left);
        top = Math.max(top, clip.top);
        right = Math.min(right, clip.right);
        bottom = Math.min(bottom, clip.bottom);
      }
      // Nothing of it left to press, once scrolled as far as it goes: a
      // control pushed out of every view is not one that overlaps another.
      if (right - left < 1 || bottom - top < 1) continue;
      const x = (left + right) / 2;
      const y = (top + bottom) / 2;
      const hit = document.elementFromPoint(x, y);
      // Only a label may answer for a control it wraps: any other ancestor
      // found at the centre means the control lets the pointer through, and
      // a press there misses it.
      if (!hit || hit === el || el.contains(hit) || (hit.tagName === "LABEL" && hit.contains(el))) {
        continue;
      }
      failures.push(`${describe(el)} is covered by ${describe(hit)}`);
    }
    return failures;
  });
}

/**
 * The widest a screen's content is, in pixels: the page's own container
 * (`[data-measure]`) on a signed-in screen, or the blocks inside `main` on a
 * signed-out one, which has no such container. Throws when the screen has
 * neither, so a screen that rendered nothing cannot pass the width check.
 */
export async function contentWidth(page: Page): Promise<number> {
  const widths = await page.evaluate(() => {
    const measured = [...document.querySelectorAll("[data-measure]")];
    const blocks = measured.length > 0 ? measured : [...document.querySelectorAll("main > *")];
    return blocks.map((el) => el.getBoundingClientRect().width);
  });
  if (widths.length === 0) throw new Error("the screen has no content to measure");
  return Math.max(...widths);
}
