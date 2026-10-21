import { readStored, writeStored } from "./storage";

/**
 * What the console remembers in this browser between visits. None of it is
 * sent to the server: it is how this browser shows the console, not who the
 * member is.
 */
const keys = {
  sidebarCollapsed: "flagnetic.sidebar-collapsed",
} as const;

export function sidebarCollapsed(): boolean {
  return readStored(keys.sidebarCollapsed) === "true";
}

export function rememberSidebarCollapsed(collapsed: boolean): void {
  writeStored(keys.sidebarCollapsed, collapsed ? "true" : "false");
}
