import { Link, useNavigate } from "@tanstack/react-router";
import type { ReactNode } from "react";

import type { WebhookScope } from "@/lib/webhooks";

/** The link to one subscription's screen, in the project's settings or the instance area. */
export function WebhookLink({
  scope,
  webhook,
  search,
  className,
  children,
}: {
  scope: WebhookScope;
  webhook: string;
  search?: { tab: "deliveries" };
  className?: string;
  children: ReactNode;
}) {
  return scope === "instance" ? (
    <Link to="/admin/webhooks/$webhook" params={{ webhook }} search={search} className={className}>
      {children}
    </Link>
  ) : (
    <Link
      to="/projects/$project/webhooks/$webhook"
      params={{ project: scope.project, webhook }}
      search={search}
      className={className}
    >
      {children}
    </Link>
  );
}

/** Opens one subscription's screen, or the list they are all in. */
export function useWebhookNavigation(scope: WebhookScope) {
  const navigate = useNavigate();
  return {
    open: (webhook: string) =>
      void (scope === "instance"
        ? navigate({ to: "/admin/webhooks/$webhook", params: { webhook } })
        : navigate({
            to: "/projects/$project/webhooks/$webhook",
            params: { project: scope.project, webhook },
          })),
    list: () =>
      void (scope === "instance"
        ? navigate({ to: "/admin/webhooks" })
        : navigate({ to: "/projects/$project/webhooks", params: { project: scope.project } })),
  };
}
