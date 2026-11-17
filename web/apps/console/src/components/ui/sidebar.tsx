import { useLingui } from "@lingui/react/macro";
import { PanelLeftCloseIcon, PanelLeftOpenIcon, PanelLeftIcon } from "lucide-react";
import { Dialog as DialogPrimitive } from "radix-ui";
import {
  createContext,
  type ComponentProps,
  type ReactNode,
  useCallback,
  useContext,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/cn";
import { rememberSidebarCollapsed, sidebarCollapsed } from "@/lib/preferences";
import { useReturnFocus } from "@/lib/return-focus";

/** Under this width the sidebar is a drawer instead of a column. */
// eslint-disable-next-line lingui/no-unlocalized-strings -- a media query, not text
const drawerQuery = "(max-width: 767px)";

function subscribeToDrawerWidth(notify: () => void): () => void {
  const query = typeof window.matchMedia === "function" ? window.matchMedia(drawerQuery) : null;
  query?.addEventListener("change", notify);
  return () => query?.removeEventListener("change", notify);
}

function isDrawerWidth(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia(drawerQuery).matches;
}

type SidebarState = {
  /** Whether the sidebar is a drawer, because the window is narrow. */
  drawer: boolean;
  /** Whether the column is the icon rail. A drawer is never one. */
  collapsed: boolean;
  /** Whether the drawer is open. */
  drawerOpen: boolean;
  setDrawerOpen: (open: boolean) => void;
  /** Collapses or expands the column, and remembers the choice in this browser. */
  toggleCollapsed: () => void;
};

const SidebarContext = createContext<SidebarState | null>(null);

/** The sidebar's state, for what is rendered inside a `SidebarProvider`. */
export function useSidebar(): SidebarState {
  const state = useContext(SidebarContext);
  if (state === null) throw new Error("useSidebar needs a SidebarProvider around it.");
  return state;
}

/**
 * Holds what the sidebar and the controls around it share: whether the
 * column is the icon rail, which this browser remembers, and whether the
 * drawer a narrow window has instead is open.
 */
export function SidebarProvider({ children }: { children: ReactNode }) {
  const drawer = useSyncExternalStore(subscribeToDrawerWidth, isDrawerWidth, () => false);
  const [collapsed, setCollapsed] = useState(sidebarCollapsed);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const toggleCollapsed = useCallback(() => {
    setCollapsed((was) => {
      rememberSidebarCollapsed(!was);
      return !was;
    });
  }, []);
  const state = useMemo<SidebarState>(
    () => ({
      drawer,
      collapsed: collapsed && !drawer,
      // A window widened past the drawer's width leaves it closed.
      drawerOpen: drawer && drawerOpen,
      setDrawerOpen,
      toggleCollapsed,
    }),
    [drawer, collapsed, drawerOpen, toggleCollapsed],
  );
  return <SidebarContext.Provider value={state}>{children}</SidebarContext.Provider>;
}

/**
 * The sidebar: a 256 px column on the deepest surface, a 48 px icon rail
 * when collapsed, and under 768 px a drawer from the left edge that traps
 * focus while open and gives it back to what opened it.
 */
export function Sidebar({ children, className }: { children: ReactNode; className?: string }) {
  const { t } = useLingui();
  const { drawer, collapsed, drawerOpen, setDrawerOpen } = useSidebar();
  const focus = useReturnFocus();
  const column = (
    <aside
      aria-label={t`Sidebar`}
      data-collapsed={collapsed ? "" : undefined}
      className={cn("flex h-full min-h-0 flex-col gap-1 p-2", className)}
    >
      {children}
    </aside>
  );
  if (drawer) {
    return (
      <DialogPrimitive.Root open={drawerOpen} onOpenChange={setDrawerOpen}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/50" />
          <DialogPrimitive.Content
            {...focus}
            aria-describedby={undefined}
            className="fixed inset-y-0 left-0 z-50 w-72 max-w-[85vw] bg-sidebar text-foreground shadow-lg"
          >
            <DialogPrimitive.Title className="sr-only">{t`Menu`}</DialogPrimitive.Title>
            {column}
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    );
  }
  return (
    <div
      data-collapsed={collapsed ? "" : undefined}
      className={cn(
        "shrink-0 transition-[width] motion-reduce:transition-none",
        collapsed ? "w-12" : "w-64",
      )}
    >
      {column}
    </div>
  );
}

/**
 * The rest of the window: one rounded card on the sidebar's surface that
 * holds the header and the page. It is flush with the window's edges under
 * 768 px, where the sidebar is not beside it.
 */
export function SidebarInset({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-1 flex-col overflow-hidden bg-background md:m-2 md:ml-0 md:rounded-lg md:border md:shadow-card",
        className,
      )}
    >
      {children}
    </div>
  );
}

/**
 * The header's button for the sidebar: it collapses the column to its
 * rail and expands it again, and in a narrow window opens the drawer.
 */
export function SidebarTrigger() {
  const { t } = useLingui();
  const { drawer, collapsed, drawerOpen, setDrawerOpen, toggleCollapsed } = useSidebar();
  if (drawer) {
    return (
      <Button
        variant="ghost"
        size="icon"
        aria-label={t`Open the sidebar`}
        aria-expanded={drawerOpen}
        onClick={() => setDrawerOpen(true)}
      >
        <PanelLeftIcon aria-hidden />
      </Button>
    );
  }
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={collapsed ? t`Expand the sidebar` : t`Collapse the sidebar`}
      aria-expanded={!collapsed}
      onClick={toggleCollapsed}
    >
      {collapsed ? <PanelLeftOpenIcon aria-hidden /> : <PanelLeftCloseIcon aria-hidden />}
    </Button>
  );
}

/**
 * A labelled group of links: 24 px above and 8 below its label, which is
 * sentence case and drops out of the rail.
 */
export function SidebarGroup({
  label,
  children,
  className,
}: {
  label?: string;
  children: ReactNode;
  className?: string;
}) {
  const { collapsed } = useSidebar();
  return (
    <div className={cn("flex flex-col gap-0.5", className)}>
      {label ? (
        <div
          className={cn(
            "px-2 pt-6 pb-2 text-xs/4 font-medium text-muted-foreground",
            collapsed && "sr-only",
          )}
        >
          {label}
        </div>
      ) : null}
      {children}
    </div>
  );
}

/**
 * The look of a sidebar link, a row of 32 px: an icon, a name that the
 * rail keeps for screen readers only, and what waits there beside it. The
 * current one is lifted onto a card.
 */
export function sidebarLinkClassName(collapsed: boolean): string {
  return cn(
    "relative flex h-8 items-center gap-2 rounded-md px-2 text-sm text-foreground-secondary transition-colors hover:bg-foreground/[0.06] hover:text-foreground aria-[current=page]:bg-card aria-[current=page]:font-medium aria-[current=page]:text-foreground aria-[current=page]:shadow-card aria-[current=page]:ring-1 aria-[current=page]:ring-border [&_svg]:size-4 [&_svg]:shrink-0",
    collapsed && "justify-center px-0",
  );
}

/** The name of a sidebar link: shown in the column, read out in the rail. */
export function SidebarLinkLabel(props: ComponentProps<"span">) {
  const { collapsed } = useSidebar();
  return <span {...props} className={cn("truncate", collapsed && "sr-only", props.className)} />;
}
