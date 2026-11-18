import { useLingui } from "@lingui/react/macro";
import { type LinkProps, useLinkProps, useRouterState } from "@tanstack/react-router";
import { ListFilterIcon, PanelLeftDashedIcon } from "lucide-react";
import { Dialog as DialogPrimitive } from "radix-ui";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";

import { Button } from "@/components/ui/button";
import { sidebarLinkClassName } from "@/components/ui/sidebar";
import { cn } from "@/lib/cn";
import { rememberContextPanelCollapsed, contextPanelCollapsed } from "@/lib/preferences";
import { useReturnFocus } from "@/lib/return-focus";

/** Under this width the context panel is a sheet instead of a column. */
// eslint-disable-next-line lingui/no-unlocalized-strings -- a media query, not text
const sheetQuery = "(max-width: 1023px)";

/** From this width a list's panel starts open: the table needs the room below it. */
// eslint-disable-next-line lingui/no-unlocalized-strings -- a media query, not text
const wideQuery = "(min-width: 1280px)";

function subscribeTo(query: string): (notify: () => void) => () => void {
  return (notify) => {
    const list = typeof window.matchMedia === "function" ? window.matchMedia(query) : null;
    list?.addEventListener("change", notify);
    return () => list?.removeEventListener("change", notify);
  };
}

function matches(query: string): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia(query).matches;
}

const subscribeToSheetWidth = subscribeTo(sheetQuery);
const subscribeToWideWidth = subscribeTo(wideQuery);
const isSheetWidth = () => matches(sheetQuery);
const isWideWidth = () => matches(wideQuery);

type ContextPanelState = {
  /** Whether the panel is a sheet, because the window is narrow. */
  sheet: boolean;
  /** Whether the column is folded away. A sheet is never folded. */
  collapsed: boolean;
  /** Whether the sheet is open. */
  sheetOpen: boolean;
  setSheetOpen: (open: boolean) => void;
  /** Folds the column away or brings it back, and remembers the choice in this browser. */
  toggleCollapsed: () => void;
  /** What the mounted panel is named, or null while the route declares none. */
  label: string | null;
  setLabel: (label: string | null) => void;
  /** Whether the mounted panel starts folded away in a window narrower than 1280 px. */
  setStartsOpenWide: (startsOpenWide: boolean) => void;
  /** Opens the panel wherever it is: the sheet in a narrow window, else the column. */
  open: () => void;
};

const ContextPanelContext = createContext<ContextPanelState | null>(null);

function useContextPanel(): ContextPanelState {
  const state = useContext(ContextPanelContext);
  if (state === null) throw new Error("A context panel needs a ContextPanelProvider around it.");
  return state;
}

/**
 * Holds what the panel and the header's button for it share: whether the
 * column is folded away, which this browser remembers, whether the sheet a
 * narrow window has instead is open, and whether a route declared a panel.
 */
export function ContextPanelProvider({ children }: { children: ReactNode }) {
  const sheet = useSyncExternalStore(subscribeToSheetWidth, isSheetWidth, () => false);
  const wide = useSyncExternalStore(subscribeToWideWidth, isWideWidth, () => true);
  // What this browser chose, or null while it has not: a list's panel then
  // starts open only in a wide window, any other panel always open.
  const [chosen, setChosen] = useState(contextPanelCollapsed);
  const [startsOpenWide, setStartsOpenWide] = useState(false);
  const collapsed = chosen ?? (startsOpenWide && !wide);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [label, setLabel] = useState<string | null>(null);
  // A window widened past the sheet's width closes the sheet for good, so
  // narrowing it again does not bring the sheet back over the page.
  const [wasSheet, setWasSheet] = useState(sheet);
  if (sheet !== wasSheet) {
    setWasSheet(sheet);
    if (!sheet) setSheetOpen(false);
  }
  const toggleCollapsed = useCallback(() => {
    const next = !collapsed;
    rememberContextPanelCollapsed(next);
    setChosen(next);
  }, [collapsed]);
  const open = useCallback(() => {
    if (sheet) setSheetOpen(true);
    else if (collapsed) toggleCollapsed();
  }, [sheet, collapsed, toggleCollapsed]);
  const state = useMemo<ContextPanelState>(
    () => ({
      sheet,
      collapsed: collapsed && !sheet,
      sheetOpen: sheet && sheetOpen,
      setSheetOpen,
      toggleCollapsed,
      label,
      setLabel,
      setStartsOpenWide,
      open,
    }),
    [sheet, collapsed, sheetOpen, toggleCollapsed, label, open],
  );
  return <ContextPanelContext.Provider value={state}>{children}</ContextPanelContext.Provider>;
}

/**
 * The second column of the shell, which a route fills: a 256 px
 * navigation landmark on its own scroll, folded away by the header's
 * toggle, and under 1024 px a sheet from the left edge that traps focus
 * while open and gives it back to what opened it. Rendered by the route's
 * declaration, so a route without one leaves the page the whole card.
 */
export function ContextPanel({
  label,
  startsOpenWide = false,
  children,
}: {
  label: string;
  /** A list's panel: it starts folded away in a window narrower than 1280 px. */
  startsOpenWide?: boolean;
  children: ReactNode;
}) {
  const { sheet, collapsed, sheetOpen, setSheetOpen, setLabel, setStartsOpenWide } =
    useContextPanel();
  const focus = useReturnFocus();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  useLayoutEffect(() => {
    setLabel(label);
    return () => setLabel(null);
  }, [label, setLabel]);
  useLayoutEffect(() => {
    setStartsOpenWide(startsOpenWide);
    return () => setStartsOpenWide(false);
  }, [startsOpenWide, setStartsOpenWide]);
  // A sheet closes once a link has taken the member elsewhere.
  useEffect(() => {
    setSheetOpen(false);
  }, [pathname, setSheetOpen]);
  const navigation = (
    <nav aria-label={label} className="flex flex-col gap-1 p-2">
      {children}
    </nav>
  );
  if (sheet) {
    return (
      <DialogPrimitive.Root open={sheetOpen} onOpenChange={setSheetOpen}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/50" />
          <DialogPrimitive.Content
            {...focus}
            aria-describedby={undefined}
            className="fixed inset-y-0 left-0 z-50 w-72 max-w-[85vw] overflow-y-auto bg-background text-foreground shadow-lg"
          >
            <DialogPrimitive.Title className="sr-only">{label}</DialogPrimitive.Title>
            {navigation}
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
    );
  }
  if (collapsed) return null;
  return <div className="w-64 shrink-0 overflow-y-auto border-r">{navigation}</div>;
}

/**
 * The header's button for the context panel: it folds the column away and
 * brings it back, and in a narrow window opens the sheet under the area's
 * name. It is absent while the route declares no panel.
 */
export function ContextPanelTrigger() {
  const { t } = useLingui();
  const { sheet, collapsed, sheetOpen, setSheetOpen, toggleCollapsed, label } = useContextPanel();
  if (label === null) return null;
  if (sheet) {
    return (
      <Button
        variant="ghost"
        size="sm"
        aria-haspopup="dialog"
        aria-expanded={sheetOpen}
        onClick={() => setSheetOpen(true)}
      >
        <PanelLeftDashedIcon aria-hidden />
        {label}
      </Button>
    );
  }
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={collapsed ? t`Expand the panel` : t`Collapse the panel`}
      aria-expanded={!collapsed}
      onClick={toggleCollapsed}
    >
      <PanelLeftDashedIcon aria-hidden />
    </Button>
  );
}

/**
 * A list toolbar's button for the panel's facets, shown while the panel is
 * folded away or is a sheet: it brings the panel back. It is absent while
 * the column is open or the route declares no panel.
 */
export function ContextPanelOpener({ children }: { children: ReactNode }) {
  const state = useContext(ContextPanelContext);
  if (state === null) return null;
  const { sheet, collapsed, label, open } = state;
  if (label === null || !(sheet || collapsed)) return null;
  return (
    <Button variant="outline" size="sm" onClick={open}>
      <ListFilterIcon aria-hidden />
      {children}
    </Button>
  );
}

/**
 * Whether the route's panel is out of sight, folded away or kept in a sheet
 * by a narrow window, so a screen shows beside its title what the panel
 * would have shown. False while the route declares no panel.
 */
export function useContextPanelHidden(): boolean {
  const state = useContext(ContextPanelContext);
  return state !== null && state.label !== null && (state.sheet || state.collapsed);
}

/** A labelled group of links in the panel; without a label, links alone. */
export function PanelGroup({ label, children }: { label?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      {label ? (
        <div className="px-2 pt-4 pb-1 text-xs/4 font-medium text-muted-foreground">{label}</div>
      ) : null}
      {children}
    </div>
  );
}

export type PanelLinkProps = LinkProps & {
  children: ReactNode;
  /** How many of what the screen lists wait there, when that is worth a number. */
  count?: number;
  /** A mark for what at the screen needs attention, shown at the link's end. */
  attention?: ReactNode;
  /**
   * Whether this is the view the screen shows, for a link whose address
   * differs from the screen's only by search parameters, which the router
   * would count as current whenever it is a part of them.
   */
  current?: boolean;
};

/** A route of the area: a 32 px row, the current one lifted onto a card. */
export function PanelLink({ children, count, attention, current, ...link }: PanelLinkProps) {
  const props = useLinkProps(link);
  return (
    <a
      {...props}
      // The router marks a link current whenever its address is part of the
      // screen's; a view that says otherwise wins.
      aria-current={current === undefined ? props["aria-current"] : current ? "page" : undefined}
      className={cn(sidebarLinkClassName(false), "justify-start")}
    >
      <span className="truncate">{children}</span>
      {count !== undefined && count > 0 ? (
        <span className="ml-auto text-xs text-muted-foreground tabular-nums">{count}</span>
      ) : null}
      {attention ? <span className="ml-auto flex items-center">{attention}</span> : null}
    </a>
  );
}
