import type { ComponentProps } from "react";

import { cn } from "@/lib/cn";

/**
 * A placeholder shaped like the thing it stands in for. Loading is never a
 * full-page spinner. It is hidden from assistive technology, so it names
 * itself in data-slot for whatever must know a screen is still loading.
 */
export function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      aria-hidden="true"
      data-slot="skeleton"
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  );
}
