import type { ComponentProps } from "react";

import { cn } from "@/lib/cn";

export type PageProps = Omit<ComponentProps<"div">, "className"> & {
  /**
   * How wide the content runs: "full" fills the page up to 1600 px, for
   * lists and objects; "form" keeps a 672 px reading measure, for forms
   * and short notices. Both are left-aligned, so the left edge of content
   * is the same on every screen.
   */
  measure?: "full" | "form";
  /**
   * Leaves room for the tester docked over the bottom of a narrow screen
   * and beside the page on a wide one, so the editor stays in reach.
   */
  reserve?: boolean;
  /**
   * A strip above a screen, such as a banner: no padding below it, the
   * screen's own top padding is the gap.
   */
  band?: boolean;
};

/**
 * The container every screen renders through, including its loading,
 * error, not-found and forbidden states: it owns the width, the page
 * padding (24 px, 16 px under 640 px) and the 20 px between blocks, so no
 * screen sets its own.
 */
export function Page({ measure = "full", reserve = false, band = false, ...props }: PageProps) {
  return (
    <div
      {...props}
      data-measure={measure}
      className={cn(
        "flex w-full flex-col gap-5 p-6 max-sm:p-4",
        measure === "full" ? "max-w-[1600px]" : "max-w-2xl",
        band && "pb-0 max-sm:pb-0",
        reserve && "pb-[62dvh] lg:pr-[27rem] lg:pb-6",
      )}
    />
  );
}
