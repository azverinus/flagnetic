import type { ComponentProps } from "react";

import { cn } from "@/lib/cn";

/**
 * A placeholder shaped like the thing it stands in for. Loading is never a
 * full-page spinner.
 */
export function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      aria-hidden="true"
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  );
}
