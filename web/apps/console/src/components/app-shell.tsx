import { Trans } from "@lingui/react/macro";
import { useMatches, useRouterState } from "@tanstack/react-router";
import { type ReactNode, useEffect } from "react";

import { Breadcrumb } from "@/components/breadcrumb";
import { InstanceLink, ProjectNavigation } from "@/components/navigation";
import { PersonalMenu } from "@/components/personal-menu";
import { ProjectSwitcher } from "@/components/project-switcher";
import { ContextPanelProvider, ContextPanelTrigger } from "@/components/ui/context-panel";
import {
  Sidebar,
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { useShellProject } from "@/lib/use-shell-project";

/**
 * The frame of every signed-in page, in zones: the sidebar on the deepest
 * surface, and the rest of the window as one inset card with a 48 px header
 * (the sidebar's toggle and the breadcrumb) over the page. The page's
 * column is positioned, so what anchors to it (the bar of unsaved changes)
 * never covers the sidebar.
 */
export function AppShell({ children }: { children: ReactNode }) {
  return (
    <SidebarProvider>
      <ContextPanelProvider>
        <div className="flex h-dvh overflow-hidden bg-sidebar">
          <a
            href="#main"
            className="sr-only z-50 rounded-md bg-background px-3 py-2 focus:not-sr-only focus:absolute focus:top-2 focus:left-2"
          >
            <Trans>Skip to content</Trans>
          </a>
          <ShellSidebar />
          <SidebarInset>
            <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
              <SidebarTrigger />
              <ContextPanelTrigger />
              <Breadcrumb />
            </header>
            <div className="flex min-h-0 flex-1">
              <DeclaredPanel />
              <main id="main" tabIndex={-1} className="relative min-w-0 flex-1 overflow-auto">
                {children}
              </main>
            </div>
          </SidebarInset>
        </div>
      </ContextPanelProvider>
    </SidebarProvider>
  );
}

/**
 * The panel the deepest matched route declares, if any: a route without
 * one leaves the page the whole card.
 */
function DeclaredPanel() {
  const Panel = useMatches().findLast((match) => match.staticData.panel)?.staticData.panel;
  return Panel ? <Panel /> : null;
}

/**
 * What the sidebar holds, top to bottom: the project switcher, the
 * project's screens, then the link to the instance and the account row.
 */
function ShellSidebar() {
  const { collapsed, setDrawerOpen } = useSidebar();
  const { key: project } = useShellProject();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  // A drawer closes once a link has taken the member elsewhere.
  useEffect(() => {
    setDrawerOpen(false);
  }, [pathname, setDrawerOpen]);
  return (
    <Sidebar>
      <ProjectSwitcher />
      {project ? <ProjectNavigation project={project} /> : <div className="flex-1" />}
      <div className="flex flex-col gap-0.5">
        <InstanceLink />
        <PersonalMenu collapsed={collapsed} />
      </div>
    </Sidebar>
  );
}
