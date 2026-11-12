import { exportProject } from "@flagnetic/api";

import { unwrap } from "./errors";

/** The encodings the server writes a project document in. */
export type DocumentFormat = "json" | "yaml";

export const documentFormats: DocumentFormat[] = ["json", "yaml"];

const types: Record<DocumentFormat, string> = {
  json: "application/json",
  yaml: "application/yaml",
};

/**
 * The name the server gave the attachment, or `{project key}.{ext}` when
 * the header is not readable.
 */
export function documentFileName(
  disposition: string | null,
  project: string,
  format: DocumentFormat,
): string {
  const named = /filename="?([^";]+)"?/i.exec(disposition ?? "")?.[1];
  return named ?? `${project}.${format}`;
}

/**
 * Downloads the project's document in the format chosen and hands it to
 * the browser as a file, under the name the server gave it.
 */
export async function downloadProjectDocument(
  project: string,
  format: DocumentFormat,
): Promise<void> {
  const settled = await exportProject({
    path: { project },
    query: { format },
    parseAs: "blob",
  });
  const body = (await unwrap(Promise.resolve(settled))) as unknown as Blob;
  const url = URL.createObjectURL(new Blob([body], { type: types[format] }));
  const link = Object.assign(window.document.createElement("a"), {
    href: url,
    download: documentFileName(
      settled.response?.headers.get("content-disposition") ?? null,
      project,
      format,
    ),
  });
  link.click();
  // Some browsers start saving only after the click returns, and a URL
  // revoked before then saves nothing; it is released on the next tick.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
