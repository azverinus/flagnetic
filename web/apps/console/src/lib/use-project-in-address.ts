import type { Project } from "@flagnetic/api";
import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";

import { projectQuery } from "./queries";

/**
 * The project the address names, for what renders outside the project's
 * own routes, such as the breadcrumb and the switcher: its key, and the
 * project once read. Nothing is read where the address names none.
 */
export function useProjectInAddress(): { key: string | undefined; project: Project | undefined } {
  const { project: key } = useParams({ strict: false });
  const { data } = useQuery({ ...projectQuery(key ?? ""), enabled: key !== undefined });
  return { key, project: data };
}
