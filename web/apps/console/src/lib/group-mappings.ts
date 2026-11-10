import {
  type GroupMapping,
  type GroupMappings,
  type GroupMappingsRun,
  getAuthProviderGroupMappings,
  replaceAuthProviderGroupMappings,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg, plural } from "@lingui/core/macro";
import { queryOptions } from "@tanstack/react-query";

import { formatNumber } from "@/i18n/format";

import { unwrap } from "./errors";
import { authProvidersKey } from "./auth-providers";

/** A provider's group mappings, in the order it evaluates them. */
export function groupMappingsQuery(slug: string) {
  return queryOptions<GroupMappings>({
    queryKey: [...authProvidersKey, slug, "group-mappings"],
    queryFn: () => unwrap(getAuthProviderGroupMappings({ path: { provider: slug } })),
  });
}

/** Replaces the mappings, which the server runs at once for every member the provider knows. */
export function replaceGroupMappings(slug: string, body: GroupMappings): Promise<GroupMappingsRun> {
  return unwrap(replaceAuthProviderGroupMappings({ path: { provider: slug }, body }));
}

/** What a mapping grants: a role in a project, or instance admin. */
export type MappingTarget = "role" | "instance_admin";

/** One mapping as its row holds it; the project and role are kept while the row grants instance admin. */
export type MappingRow = {
  /** Identifies the row while it moves, as the position of a list is not stable. */
  id: string;
  group: string;
  target: MappingTarget;
  project: string;
  role: string;
};

let rowCount = 0;

/** A new row: no group, and a role in a project not chosen yet. */
export function newRow(): MappingRow {
  rowCount += 1;
  return { id: `mapping-${rowCount}`, group: "", target: "role", project: "", role: "" };
}

/** The rows of the mappings a provider holds, in their order. */
export function mappingRows(items: readonly GroupMapping[]): MappingRow[] {
  return items.map((item) => ({
    ...newRow(),
    group: item.group,
    target: item.instance_admin === true ? "instance_admin" : "role",
    project: item.project ?? "",
    role: item.role ?? "",
  }));
}

function mappingOf(row: MappingRow): GroupMapping {
  const group = row.group.trim();
  return row.target === "instance_admin"
    ? { group, instance_admin: true }
    : { group, project: row.project, role: row.role };
}

/** The request that replaces the mappings with what the rows hold. */
export function bodyOf(rows: readonly MappingRow[]): GroupMappings {
  return { items: rows.map(mappingOf) };
}

/** Whether the rows send exactly the mappings saved. */
export function sameMappings(rows: readonly MappingRow[], saved: readonly GroupMapping[]): boolean {
  const sent = bodyOf(rows).items;
  return (
    sent.length === saved.length &&
    sent.every(
      (item, at) =>
        item.group === saved[at]?.group &&
        item.project === saved[at]?.project &&
        item.role === saved[at]?.role &&
        item.instance_admin === saved[at]?.instance_admin,
    )
  );
}

/** Moves the row at `index` one place up (-1) or down (1); the rows themselves at either end. */
export function moveRow(rows: MappingRow[], index: number, by: -1 | 1): MappingRow[] {
  const to = index + by;
  if (to < 0 || to >= rows.length) return rows;
  const moved = [...rows];
  const [row] = moved.splice(index, 1);
  moved.splice(to, 0, row!);
  return moved;
}

export type RowErrors = Partial<Record<"group" | "project" | "role", string>>;

/** What each row gets wrong that the server would refuse, by field; none for a row that is complete. */
export function checkRows(i18n: I18n, rows: readonly MappingRow[]): (RowErrors | undefined)[] {
  return rows.map((row) => {
    const errors: RowErrors = {};
    if (row.group.trim() === "") errors.group = i18n._(msg`Enter the directory group.`);
    if (row.target === "role") {
      if (row.project === "") errors.project = i18n._(msg`Choose a project.`);
      if (row.role === "") errors.role = i18n._(msg`Choose a role.`);
    }
    return Object.keys(errors).length > 0 ? errors : undefined;
  });
}

/** What running the replaced mappings did, as a sentence in the member's language. */
export function runSummary(i18n: I18n, run: GroupMappingsRun): string {
  const granted = formatNumber(i18n, run.granted);
  const revoked = formatNumber(i18n, run.revoked);
  const done = i18n._(msg`Granted ${granted}, removed ${revoked}.`);
  if (run.kept_instance_admin === 0) return done;
  const kept = i18n._(
    msg`${plural(run.kept_instance_admin, {
      one: "# member stays instance admin, being the last active one.",
      other: "# members stay instance admins, being the last active ones.",
    })}`,
  );
  return `${done} ${kept}`;
}
