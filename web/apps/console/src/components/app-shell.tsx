import { Trans, useLingui } from "@lingui/react/macro";
import { Link } from "@tanstack/react-router";
import { HouseIcon, PanelLeftCloseIcon, PanelLeftOpenIcon } from "lucide-react";
import { type ReactNode, useState } from "react";

import { Breadcrumb } from "@/components/breadcrumb";
import { PersonalMenu } from "@/components/personal-menu";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { rememberSidebarCollapsed, sidebarCollapsed } from "@/lib/preferences";

/**
 * The frame of every signed-in page: the sidebar, the header with the
 * breadcrumb and the personal menu, and the main column the page renders
 * in. The main column is positioned, so what anchors to it (the bar of
 * unsaved changes) never covers the sidebar.
 */
export function AppShell({ children }: { children: ReactNode }) {
  const { t } = useLingui();
  const [collapsed, setCollapsed] = useState(sidebarCollapsed);
  const toggle = () => {
    rememberSidebarCollapsed(!collapsed);
    setCollapsed(!collapsed);
  };

  return (
    <div className="flex h-dvh overflow-hidden">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-background px-3 py-2 focus:not-sr-only focus:absolute focus:top-2 focus:left-2"
      >
        <Trans>Skip to content</Trans>
      </a>
      <aside
        aria-label={t`Sidebar`}
        data-collapsed={collapsed ? "" : undefined}
        className={cn(
          "flex shrink-0 flex-col border-r bg-card transition-[width]",
          collapsed ? "w-14" : "w-14 md:w-60",
        )}
      >
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <img src="/favicon.svg" alt="" className="size-6 shrink-0" />
          <span
            className={cn("truncate font-semibold", collapsed ? "sr-only" : "hidden md:inline")}
          >
            Flagnetic
          </span>
        </div>
        <nav aria-label={t`Main`} className="flex flex-1 flex-col gap-1 p-2">
          <Link
            to="/"
            activeOptions={{ exact: true }}
            className="flex h-9 items-center gap-2 rounded-md px-2.5 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground data-[status=active]:bg-accent data-[status=active]:font-medium data-[status=active]:text-foreground"
          >
            <HouseIcon aria-hidden className="size-4 shrink-0" />
            <span className={cn("truncate", collapsed ? "sr-only" : "sr-only md:not-sr-only")}>
              <Trans>Home</Trans>
            </span>
          </Link>
        </nav>
        <div className="hidden border-t p-2 md:block">
          <Button
            variant="ghost"
            size="icon"
            onClick={toggle}
            aria-label={collapsed ? t`Expand the sidebar` : t`Collapse the sidebar`}
            aria-expanded={!collapsed}
          >
            {collapsed ? <PanelLeftOpenIcon aria-hidden /> : <PanelLeftCloseIcon aria-hidden />}
          </Button>
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
          <Breadcrumb />
          <div className="ml-auto shrink-0">
            <PersonalMenu />
          </div>
        </header>
        <main id="main" tabIndex={-1} className="relative min-w-0 flex-1 overflow-auto">
          {children}
        </main>
      </div>
    </div>
  );
}
