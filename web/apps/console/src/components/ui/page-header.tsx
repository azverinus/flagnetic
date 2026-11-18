import { useLingui } from "@lingui/react/macro";
import { EllipsisIcon } from "lucide-react";
import { type ReactNode, useEffect, useRef } from "react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/cn";

export type PageHeaderProps = {
  title: string;
  /** Above the title: the way up from this page, when it is not the shell's breadcrumb. */
  breadcrumb?: ReactNode;
  description?: ReactNode;
  /**
   * The page's buttons, the primary one last. At most one is filled; the
   * rest are outlined, and a destructive one is outlined too. With three
   * or more secondary actions, keep one or two here and put the rest in
   * `more`.
   */
  actions?: ReactNode;
  /**
   * Menu items (`DropdownMenuItem`) for the rarer actions, behind one
   * "More actions" button after `actions`. A destructive one belongs here
   * or outlined in `actions`, never filled.
   */
  more?: ReactNode;
  className?: string;
};

/**
 * The top of a page: breadcrumb, title and actions. A long title is cut
 * with an ellipsis and stays readable in full on hover and to a screen
 * reader; the actions wrap under the title on a narrow screen, every
 * button at one height, and at most one of them is filled.
 */
export function PageHeader({
  title,
  breadcrumb,
  description,
  actions,
  more,
  className,
}: PageHeaderProps) {
  const { t } = useLingui();
  const bar = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (import.meta.env.PROD) return;
    const filled = bar.current?.querySelectorAll('[data-variant="default"]').length ?? 0;
    if (filled > 1) {
      console.error(`PageHeader "${title}" has ${filled} filled buttons; at most one is allowed.`);
    }
  });
  return (
    <header className={cn("flex min-w-0 flex-col gap-2", className)}>
      {breadcrumb}
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h1 className="min-w-0 flex-1 truncate text-2xl/7 font-semibold" title={title}>
          {title}
        </h1>
        {actions || more ? (
          <div ref={bar} className="flex flex-wrap items-center gap-2 [&>*]:h-8">
            {actions}
            {more ? (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" size="icon" aria-label={t`More actions`}>
                    <EllipsisIcon aria-hidden />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">{more}</DropdownMenuContent>
              </DropdownMenu>
            ) : null}
          </div>
        ) : null}
      </div>
      {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
    </header>
  );
}
