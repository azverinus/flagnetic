import { exportAuditEntries, exportProjectAuditEntries } from "@flagnetic/api";

import { type AuditSearch, auditQueryOf } from "./audit";
import { unwrap } from "./errors";

/** The formats the server writes an audit download in. */
export type AuditFormat = "jsonl" | "csv";

/** The scope of an audit log: one project's, or the instance's. */
export type AuditFileScope = { project: string } | "instance";

const types: Record<AuditFormat, string> = {
  jsonl: "application/x-ndjson",
  csv: "text/csv",
};

/**
 * The name the server gave the attachment, or the one it documents
 * (`audit-{project key|instance}-{UTC time}.{ext}`) when the header is
 * not readable.
 */
export function auditFileName(
  disposition: string | null,
  scope: AuditFileScope,
  format: AuditFormat,
  now: Date = new Date(),
): string {
  const named = /filename="?([^";]+)"?/i.exec(disposition ?? "")?.[1];
  if (named) return named;
  const stamp = now
    .toISOString()
    .replace(/\.\d+Z$/, "Z")
    .replace(/[-:]/g, "");
  return `audit-${scope === "instance" ? "instance" : scope.project}-${stamp}.${format}`;
}

/**
 * Downloads the entries the audit log's filters match, in the format
 * chosen, and hands them to the browser as a file. The page the member
 * is on is no filter: the download is of every matching entry.
 */
export async function downloadAudit(
  scope: AuditFileScope,
  search: AuditSearch,
  format: AuditFormat,
): Promise<void> {
  const { cursor: _page, ...filters } = search;
  const result =
    scope === "instance"
      ? exportAuditEntries({
          query: { ...auditQueryOf(filters), format },
          parseAs: "blob",
        })
      : exportProjectAuditEntries({
          path: { project: scope.project },
          query: { ...auditQueryOf({ ...filters, project: undefined }), format },
          parseAs: "blob",
        });
  const settled = await result;
  const body = (await unwrap(Promise.resolve(settled))) as unknown as Blob;
  const url = URL.createObjectURL(new Blob([body], { type: types[format] }));
  const link = Object.assign(window.document.createElement("a"), {
    href: url,
    download: auditFileName(
      settled.response?.headers.get("content-disposition") ?? null,
      scope,
      format,
    ),
  });
  link.click();
  // Released on the next tick, since some browsers start saving only
  // after the click returns.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
