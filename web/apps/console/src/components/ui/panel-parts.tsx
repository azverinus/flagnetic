import { Trans } from "@lingui/react/macro";
import { LockIcon } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";

import { Lamp } from "@/components/ui/lamp";
import { PanelLink, type PanelLinkProps } from "@/components/ui/context-panel";
import { sidebarLinkClassName } from "@/components/ui/sidebar";
import { cn } from "@/lib/cn";

export type PanelEnvironmentProps = Omit<PanelLinkProps, "children" | "attention"> & {
  name: string;
  /** Whether the environment's changes go through a change request. */
  protected: boolean;
  /** Whether the object is serving in the environment. */
  on: boolean;
};

/**
 * An environment of a flag or an experiment in the context panel: its
 * name, a lock when it is protected, and the state lamp at the end of the
 * row. The open one is the current link.
 */
export function PanelEnvironment({
  name,
  protected: isProtected,
  on,
  ...link
}: PanelEnvironmentProps) {
  return (
    <PanelLink {...link} attention={<Lamp on={on} className="text-xs" />}>
      {name}
      {isProtected ? (
        <>
          <LockIcon aria-hidden className="ml-1.5 inline size-3 align-[-1px]" />{" "}
          <span className="sr-only">
            <Trans>Protected</Trans>
          </span>
        </>
      ) : null}
    </PanelLink>
  );
}

/** An action of the panel that is not a route: it looks like a link and acts as a button. */
export function PanelButton({ className, ...props }: ComponentProps<"button">) {
  return (
    <button
      type="button"
      {...props}
      className={cn(
        sidebarLinkClassName(false),
        "w-full justify-start text-left disabled:pointer-events-none disabled:opacity-50 aria-disabled:opacity-50",
        className,
      )}
    />
  );
}

/** The facts of an object, as a definition list in the panel. */
export function PanelFacts({ children }: { children: ReactNode }) {
  return <dl className="flex flex-col gap-3 px-2 py-1 text-sm">{children}</dl>;
}

/** One fact of `PanelFacts`: a muted term over its value. */
export function PanelFact({ term, children }: { term: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-xs/4 text-muted-foreground">{term}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  );
}
