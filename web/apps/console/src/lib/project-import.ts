import {
  type Environment,
  type ImportKindChanges,
  type ImportResult,
  type ImportUpdate,
  type ImportWarning,
  importNewProject,
  importProject,
  type JsonPatchOperation,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { unwrap } from "./errors";

/** How the document is applied to the project it is imported into. */
export type ImportMode = "merge" | "replace";

/** The document types the server reads, by the extension of the file. */
const mediaTypes: Record<string, string> = {
  json: "application/json",
  yaml: "application/yaml",
  yml: "application/yaml",
};

/**
 * The media type to send a file with: by its extension, since browsers
 * leave the type of a YAML file empty. Undefined for a file that is
 * neither JSON nor YAML.
 */
export function documentMediaType(fileName: string): string | undefined {
  const extension = /\.([^.]+)$/.exec(fileName)?.[1]?.toLowerCase();
  return extension === undefined ? undefined : mediaTypes[extension];
}

/** A dry run's answer with the state of the project it was judged against. */
export type DryRun = {
  result: ImportResult;
  /** The `ETag` the server answered, which the apply sends back as `If-Match`. */
  etag: string;
};

/** The document held between the steps, as the member chose it. */
export type HeldDocument = {
  name: string;
  mediaType: string;
  text: string;
};

async function send(
  project: string,
  mode: ImportMode,
  document: HeldDocument,
  dryRun: boolean,
  ifMatch?: string,
) {
  const settled = await importProject({
    path: { project },
    query: { mode, dry_run: dryRun },
    // The text goes as it was read, so the server judges the file the member chose.
    body: new Blob([document.text], { type: document.mediaType }),
    headers: {
      "Content-Type": document.mediaType,
      ...(ifMatch === undefined ? {} : { "If-Match": ifMatch }),
    },
  });
  const result = await unwrap(Promise.resolve(settled));
  return { result, etag: settled.response?.headers.get("etag") ?? "" };
}

/** What importing the document would do, with the `ETag` that pins the project's state. */
export function dryRunImport(
  project: string,
  mode: ImportMode,
  document: HeldDocument,
): Promise<DryRun> {
  return send(project, mode, document, true);
}

/** Applies the document to the project, only if it is still as the dry run found it. */
export async function applyImport(
  project: string,
  mode: ImportMode,
  document: HeldDocument,
  etag: string,
): Promise<ImportResult> {
  return (await send(project, mode, document, false, etag || undefined)).result;
}

/** What a new project is created with: the document's key and name, unless the member chose others. */
export type NewProjectIdentity = { key: string; name: string };

async function sendNew(
  document: HeldDocument,
  identity: NewProjectIdentity,
  dryRun: boolean,
): Promise<DryRun> {
  const settled = await importNewProject({
    query: { key: identity.key, name: identity.name, dry_run: dryRun },
    body: new Blob([document.text], { type: document.mediaType }),
    headers: { "Content-Type": document.mediaType },
  });
  const result = await unwrap(Promise.resolve(settled));
  return { result, etag: settled.response?.headers.get("etag") ?? "" };
}

/** What creating a project from the document would do. */
export function dryRunNewProject(
  document: HeldDocument,
  identity: NewProjectIdentity,
): Promise<DryRun> {
  return sendNew(document, identity, true);
}

/** Creates the project from the document. */
export async function createFromDocument(
  document: HeldDocument,
  identity: NewProjectIdentity,
): Promise<ImportResult> {
  return (await sendNew(document, identity, false)).result;
}

/**
 * The key and name the document gives its project, for the form to offer
 * as defaults. JSON is parsed; YAML is read line by line for the two
 * scalars under the top-level `project`, since the console carries no
 * YAML parser. Whatever cannot be read is left empty.
 */
export function documentIdentity(document: HeldDocument): NewProjectIdentity {
  const none = { key: "", name: "" };
  if (document.mediaType === "application/json") {
    try {
      const project = (JSON.parse(document.text) as { project?: Record<string, unknown> }).project;
      return {
        key: typeof project?.key === "string" ? project.key : "",
        name: typeof project?.name === "string" ? project.name : "",
      };
    } catch {
      return none;
    }
  }
  const found = { ...none };
  let inside = false;
  for (const line of document.text.split(/\r?\n/)) {
    if (/^\S/.test(line)) {
      inside = /^project:\s*(#.*)?$/.test(line);
      continue;
    }
    if (!inside) continue;
    const scalar = /^\s+(key|name):\s*(.*?)\s*$/.exec(line);
    if (scalar?.[1] === undefined || scalar[2] === undefined) continue;
    const raw = scalar[2].replace(/\s+#.*$/, "");
    const value = /^(["'])(.*)\1$/.exec(raw)?.[2] ?? raw;
    if (scalar[1] === "key") found.key = value;
    else found.name = value;
  }
  return found;
}

/** How many entities the import would delete, over every kind. */
export function deletionCount(result: ImportResult): number {
  return changeKinds.reduce((sum, kind) => sum + result.changes[kind].deleted.length, 0);
}

export type ChangeKind =
  "environments" | "unit_types" | "attributes" | "segments" | "flags" | "experiments";

export const changeKinds: ChangeKind[] = [
  "environments",
  "unit_types",
  "attributes",
  "segments",
  "flags",
  "experiments",
];

/** The kinds the import changes, in the order the page lists them; the ones it leaves alone are absent. */
export function changedKinds(
  result: ImportResult,
): { kind: ChangeKind; changes: ImportKindChanges }[] {
  return changeKinds
    .map((kind) => ({ kind, changes: result.changes[kind] }))
    .filter(
      ({ changes }) => changes.created.length + changes.updated.length + changes.deleted.length > 0,
    );
}

/** Whether the import would change nothing, which leaves nothing to apply. */
export function isUnchanged(result: ImportResult): boolean {
  return changedKinds(result).length === 0;
}

/** One operation of a patch, ready to be written as a sentence. */
export type PatchLine = {
  op: JsonPatchOperation["op"];
  /** The JSON Pointer into the entity, as the server wrote it. */
  path: string;
  from?: string;
  /** The value as JSON, shortened when it is long; absent for an operation that carries none. */
  value?: string;
};

const longestValue = 120;

/** A value of a patch as one line of JSON, cut at a length a row can hold. */
export function patchValue(value: unknown): string {
  const text = JSON.stringify(value) ?? "null";
  return text.length > longestValue ? `${text.slice(0, longestValue - 1)}…` : text;
}

/** The segments of a JSON Pointer with the escapes of RFC 6901 undone. */
export function pointerSegments(pointer: string): string[] {
  if (pointer === "") return [];
  return pointer
    .slice(1)
    .split("/")
    .map((segment) => segment.replaceAll("~1", "/").replaceAll("~0", "~"));
}

/**
 * The lines of an update's patch. A `test` operation guards the patch
 * rather than changing anything, so it is not shown.
 */
export function patchLines(update: ImportUpdate): PatchLine[] {
  return update.patch
    .filter((operation) => operation.op !== "test")
    .map((operation) => ({
      op: operation.op,
      path: operation.path === "" ? "/" : operation.path,
      ...(operation.from === undefined ? {} : { from: operation.from }),
      ...("value" in operation &&
      operation.op !== "remove" &&
      operation.op !== "move" &&
      operation.op !== "copy"
        ? { value: patchValue(operation.value) }
        : {}),
    }));
}

/**
 * The protected environments the import writes to, in the project's
 * order. An environment is touched when the import changes or deletes it,
 * when an updated flag's patch reaches into its configuration, and, since
 * a new flag is configured in every environment, when a flag is created.
 */
export function protectedEnvironmentsTouched(
  result: ImportResult,
  environments: Pick<Environment, "key" | "protected">[],
): string[] {
  const { changes } = result;
  const touched = new Set<string>([
    ...changes.environments.updated.map((update) => update.key),
    ...changes.environments.deleted,
  ]);
  if (changes.flags.created.length > 0) {
    for (const environment of environments) touched.add(environment.key);
  }
  for (const update of changes.flags.updated) {
    for (const operation of update.patch) {
      const [first, environment] = pointerSegments(operation.path);
      if (first === "configurations" && environment !== undefined) touched.add(environment);
    }
  }
  return environments
    .filter((environment) => environment.protected && touched.has(environment.key))
    .map((environment) => environment.key);
}

/** The sentence for a warning of the import, by its code; the server's own words for a code not known yet. */
export function warningMessage(i18n: I18n, warning: ImportWarning): string {
  switch (warning.code) {
    case "owner_replaced": {
      const email = warning.params?.email;
      return typeof email === "string"
        ? i18n._(
            msg`${email} may not own anything in this project, so the importing member owns it instead.`,
          )
        : i18n._(
            msg`The owner may not own anything in this project, so the importing member owns it instead.`,
          );
    }
    case "protection_kept":
      return i18n._(
        msg`The environment keeps its protection; the document's value for it is not applied.`,
      );
    default:
      return warning.detail;
  }
}

/** What the page keeps across a reload: the step and the document. */
export type ImportSession = {
  step: "upload" | "review";
  mode?: ImportMode;
  document?: HeldDocument;
};

const sessionKey = (project: string) => `flagnetic.import.${project}`;

function isImportSession(value: unknown): value is ImportSession {
  if (typeof value !== "object" || value === null) return false;
  const held = value as Partial<ImportSession>;
  const document = held.document;
  return (
    (held.step === "upload" || held.step === "review") &&
    (held.mode === undefined || held.mode === "merge" || held.mode === "replace") &&
    (document === undefined ||
      (typeof document === "object" &&
        document !== null &&
        typeof document.name === "string" &&
        typeof document.mediaType === "string" &&
        typeof document.text === "string"))
  );
}

/**
 * The session kept for the project. A step that needs the document but
 * has lost it, such as a document too large for the browser to keep,
 * returns to the upload.
 */
export function loadImportSession(project: string): ImportSession {
  const fresh: ImportSession = { step: "upload" };
  try {
    const raw = window.sessionStorage.getItem(sessionKey(project));
    if (raw === null) return fresh;
    const parsed: unknown = JSON.parse(raw);
    if (!isImportSession(parsed)) return fresh;
    return parsed.step === "review" && parsed.document === undefined
      ? { ...parsed, step: "upload" }
      : parsed;
  } catch {
    return fresh;
  }
}

/** Keeps the session, or forgets it when it is back at the start. */
export function saveImportSession(project: string, session: ImportSession): void {
  try {
    if (session.step === "upload" && session.document === undefined && session.mode !== "replace") {
      window.sessionStorage.removeItem(sessionKey(project));
    } else {
      window.sessionStorage.setItem(sessionKey(project), JSON.stringify(session));
    }
  } catch {
    // Storage may be full or blocked. What an earlier step kept is dropped,
    // so a reload returns to the upload instead of reviewing an older document.
    try {
      window.sessionStorage.removeItem(sessionKey(project));
    } catch {
      // Blocked storage keeps nothing to drop.
    }
  }
}
