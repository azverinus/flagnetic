import { useParams } from "@tanstack/react-router";

import { lastProject } from "./preferences";

/**
 * The project the sidebar shows: the one the address names, else the one
 * this browser showed last, so the sidebar is the same object on the
 * instance and personal screens. `inAddress` tells the two apart, since a
 * remembered project has no current screen.
 */
export function useShellProject(): { key: string | undefined; inAddress: boolean } {
  const { project } = useParams({ strict: false });
  if (project !== undefined) return { key: project, inAddress: true };
  return { key: lastProject() ?? undefined, inAddress: false };
}
