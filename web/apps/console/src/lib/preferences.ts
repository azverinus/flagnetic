import { readStored, writeStored } from "./storage";

/**
 * What the console remembers in this browser between visits. None of it is
 * sent to the server: it is how this browser shows the console, not who the
 * member is.
 */
const keys = {
  sidebarCollapsed: "flagnetic.sidebar-collapsed",
  lastProject: "flagnetic.last-project",
  lastEnvironment: (project: string) => `flagnetic.last-environment.${project}`,
} as const;

export function sidebarCollapsed(): boolean {
  return readStored(keys.sidebarCollapsed) === "true";
}

export function rememberSidebarCollapsed(collapsed: boolean): void {
  writeStored(keys.sidebarCollapsed, collapsed ? "true" : "false");
}

/** The key of the project this browser showed last, which the start page reopens. */
export function lastProject(): string | null {
  return readStored(keys.lastProject);
}

export function rememberLastProject(project: string): void {
  writeStored(keys.lastProject, project);
}

/**
 * The key of the environment this browser showed last in a project's
 * flags list, which the list opens in again; each project keeps its own.
 */
export function lastEnvironment(project: string): string | null {
  return readStored(keys.lastEnvironment(project));
}

export function rememberLastEnvironment(project: string, environment: string): void {
  writeStored(keys.lastEnvironment(project), environment);
}
