import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { cn } from "@/lib/cn";

const badge = cva("rounded-sm border px-1.5 py-0.5 text-xs", {
  variants: {
    variant: {
      /** Something to notice about a row, such as the device in use. */
      default: "border-primary/40 text-primary",
      /** A plain fact about a row, such as a standard attribute. */
      muted: "text-muted-foreground",
    },
  },
  defaultVariants: { variant: "default" },
});

export type BadgeProps = ComponentProps<"span"> & VariantProps<typeof badge>;

/** A short label beside a name in a row, saying what sets it apart. */
export function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badge({ variant }), className)} {...props} />;
}
