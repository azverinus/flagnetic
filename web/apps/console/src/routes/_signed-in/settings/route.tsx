import type { MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { createFileRoute, Link, type LinkProps, Outlet } from "@tanstack/react-router";

import { PageHeader } from "@/components/ui/page-header";

/** The personal settings' pages, in the order their navigation lists them. */
const pages = [
  { to: "/settings/profile", label: msg`Profile` },
  { to: "/settings/sessions", label: msg`Sessions` },
  { to: "/settings/tokens", label: msg`API tokens` },
] as const satisfies readonly { to: NonNullable<LinkProps["to"]>; label: MessageDescriptor }[];

/** The member's own account: who they are, how they sign in, and their tokens. */
export const Route = createFileRoute("/_signed-in/settings")({
  staticData: { crumb: msg`Personal settings` },
  component: PersonalSettings,
});

function PersonalSettings() {
  const { i18n, t } = useLingui();
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader title={t`Personal settings`} />
      <nav aria-label={t`Personal settings`} className="overflow-x-auto border-b">
        <ul className="flex gap-1">
          {pages.map((page) => (
            <li key={page.to}>
              <Link
                to={page.to}
                className="-mb-px inline-flex h-9 items-center border-b-2 border-transparent px-3 text-sm whitespace-nowrap text-muted-foreground hover:text-foreground"
                activeProps={{ className: "border-primary font-medium text-foreground" }}
              >
                {i18n._(page.label)}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      <Outlet />
    </div>
  );
}
