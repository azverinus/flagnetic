import type { ReactNode } from "react";

import { cn } from "@/lib/cn";

export type EmptyStateProps = {
  icon?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  /** The action that fills the emptiness, such as creating the first one. */
  action?: ReactNode;
  className?: string;
};

/**
 * What a list shows with nothing in it: what would be here and how to add it.
 * It sits inside the list's card, which gives it the surface.
 */
export function EmptyState({ icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center",
        className,
      )}
    >
      {icon ? (
        <div aria-hidden className="text-muted-foreground [&_svg]:size-8">
          {icon}
        </div>
      ) : null}
      <p className="font-medium">{title}</p>
      {description ? <p className="max-w-md text-sm text-muted-foreground">{description}</p> : null}
      {action}
    </div>
  );
}
