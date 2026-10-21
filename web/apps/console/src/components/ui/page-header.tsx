import type { ReactNode } from "react";

import { cn } from "@/lib/cn";

export type PageHeaderProps = {
  title: string;
  /** Above the title: the way up from this page, when it is not the shell's breadcrumb. */
  breadcrumb?: ReactNode;
  description?: ReactNode;
  /** The page's actions, the primary one last. */
  actions?: ReactNode;
  className?: string;
};

/**
 * The top of a page: breadcrumb, title and actions. A long title is cut
 * with an ellipsis and stays readable in full on hover and to a screen
 * reader; the actions wrap under the title on a narrow screen, every
 * button at one height.
 */
export function PageHeader({
  title,
  breadcrumb,
  description,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <header className={cn("flex min-w-0 flex-col gap-2", className)}>
      {breadcrumb}
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h1 className="min-w-0 flex-1 truncate text-xl font-semibold" title={title}>
          {title}
        </h1>
        {actions ? (
          <div className="flex flex-wrap items-center gap-2 [&>*]:h-9">{actions}</div>
        ) : null}
      </div>
      {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
    </header>
  );
}
