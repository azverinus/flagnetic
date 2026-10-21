import { Trans } from "@lingui/react/macro";
import { LockIcon } from "lucide-react";
import { Children, cloneElement, type MouseEvent, type ReactElement, type ReactNode } from "react";

import { PageHeader } from "@/components/ui/page-header";
import { Tooltip } from "@/components/ui/tooltip";
import { useCan, useMissingPermissionReason } from "@/lib/access";
import type { Permission } from "@/lib/access-names";
import { cn } from "@/lib/cn";

type ActionProps = {
  className?: string;
  onClick?: (event: MouseEvent) => void;
  "aria-describedby"?: string;
  "aria-disabled"?: boolean;
};

export type CanProps = {
  /** The permission the action needs in the project around. */
  permission: Permission;
  /**
   * What to do when the caller lacks it: hide the action, or show it
   * disabled with the reason. An action is shown disabled where seeing it
   * tells the member something, such as the primary action of a screen;
   * the actions of every row are hidden, so a list does not fill up with
   * what cannot be done.
   */
  whenMissing: "hide" | "disable";
  /** The one action, a button or a link. */
  children: ReactElement<ActionProps>;
};

/** Renders an action as the caller's permissions in the project allow. */
export function Can({ permission, whenMissing, children }: CanProps) {
  const can = useCan(permission);
  if (can) return children;
  if (whenMissing === "hide") return null;
  return <DisabledAction permission={permission}>{children}</DisabledAction>;
}

/**
 * An action the caller cannot take: it stays in the tab order so a
 * keyboard reaches it, is marked disabled, does nothing when activated,
 * and carries the reason as its tooltip and description. The action
 * itself takes no pointer events, so hovering lands on the tooltip's
 * wrapper.
 */
function DisabledAction({
  permission,
  children,
}: {
  permission: Permission;
  children: ReactElement<ActionProps>;
}) {
  const reason = useMissingPermissionReason(permission);
  const action = Children.only(children);
  const refuse = (event: MouseEvent) => {
    event.preventDefault();
    event.stopPropagation();
  };

  return (
    <Tooltip content={reason}>
      {cloneElement(action, {
        "aria-disabled": true,
        onClick: refuse,
        className: cn(action.props.className, "pointer-events-none opacity-50"),
      })}
    </Tooltip>
  );
}

export type PermissionGateProps = {
  permission: Permission;
  /** The screen's title, kept so the member still knows where they are. */
  title: string;
  children: ReactNode;
};

/**
 * A whole screen that needs a permission: rendered for a caller holding
 * it, and otherwise replaced by what it would take, rather than by the
 * server's refusal of its first request.
 */
export function PermissionGate({ permission, title, children }: PermissionGateProps) {
  const can = useCan(permission);
  const reason = useMissingPermissionReason(permission);
  if (can) return children;
  return <RefusedScreen title={title} reason={reason} />;
}

/** A screen the caller cannot open, with why, in place of its content. */
export function RefusedScreen({ title, reason }: { title: string; reason: string }) {
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader title={title} />
      <div
        role="alert"
        className="flex items-start gap-3 rounded-md border bg-muted/40 p-4 text-sm"
      >
        <LockIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="flex flex-col gap-1">
          <p className="font-medium">
            <Trans>You cannot open this screen</Trans>
          </p>
          <p className="text-muted-foreground">{reason}</p>
        </div>
      </div>
    </div>
  );
}
