// The tab the client runs in: whether it is visible and when it becomes
// visible, hidden or reconnected. `visibilitychange` takes the place of a
// mobile app's foreground: a tab coming back refetches, a hidden one stops
// polling. `pagehide` counts as hidden, since a tab being closed or put in
// the back-forward cache may never report `visibilitychange`.
export interface PageHandlers {
  /** The tab became visible. */
  visible(): void;
  /** The tab went hidden, or is being unloaded. */
  hidden(): void;
  /** The network came back, whether or not the tab is visible. */
  online(): void;
}

/** What this SDK uses of the page, so a test can stand in for it. */
export interface Page {
  visible(): boolean;
  /** Subscribes to the transitions. Returns a function that unsubscribes. */
  listen(handlers: PageHandlers): () => void;
}

/** A page that is always visible and never changes: Node, a worker, a test. */
export const headlessPage: Page = {
  visible: () => true,
  listen: () => () => undefined,
};

/** The document and window this code runs in, or {@link headlessPage} outside a browser. */
export function defaultPage(): Page {
  const doc = (globalThis as { document?: { visibilityState?: string; addEventListener?: unknown; removeEventListener?: unknown } }).document;
  const target = globalThis as unknown as {
    addEventListener?: (type: string, fn: () => void) => void;
    removeEventListener?: (type: string, fn: () => void) => void;
  };
  if (!doc || typeof doc.addEventListener !== "function" || typeof target.addEventListener !== "function") return headlessPage;
  const document = doc as unknown as {
    visibilityState?: string;
    addEventListener: (type: string, fn: () => void) => void;
    removeEventListener: (type: string, fn: () => void) => void;
  };
  return {
    visible: () => document.visibilityState !== "hidden",
    listen(handlers) {
      const onVisibility = (): void => {
        if (document.visibilityState === "hidden") handlers.hidden();
        else handlers.visible();
      };
      const onOnline = (): void => { handlers.online(); };
      const onHide = (): void => { handlers.hidden(); };
      document.addEventListener("visibilitychange", onVisibility);
      target.addEventListener?.("online", onOnline);
      target.addEventListener?.("pagehide", onHide);
      return () => {
        document.removeEventListener("visibilitychange", onVisibility);
        target.removeEventListener?.("online", onOnline);
        target.removeEventListener?.("pagehide", onHide);
      };
    },
  };
}
