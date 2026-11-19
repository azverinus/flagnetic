/**
 * The drafts the import wizards keep in the tab's session storage so a reload
 * resumes where the member was: the uploaded file's text among them, which may
 * be a document the member never imported. Session storage lives as long as
 * the tab, and a tab on a shared device outlives the person signed in to it,
 * so a draft belongs to the member who made it: its key names them, and it is
 * forgotten when the session ends, when anyone signs out, and when another
 * member opens the wizard.
 */
const projectPrefix = "flagnetic.import.";
const firebasePrefix = "flagnetic.import-firebase.";

/** Which wizard a draft belongs to. */
export type DraftKind = "project" | "firebase";

/** The storage key of a member's draft of one wizard in one project. */
export function draftKey(kind: DraftKind, member: string, project: string): string {
  return `${kind === "project" ? projectPrefix : firebasePrefix}${member}.${project}`;
}

function draftKeys(storage: Storage): string[] {
  const keys = Array.from({ length: storage.length }, (_, index) => storage.key(index));
  return keys.filter(
    (key): key is string =>
      key !== null && (key.startsWith(projectPrefix) || key.startsWith(firebasePrefix)),
  );
}

/** Forgets every draft of every member in this tab. */
export function forgetImportDrafts(): void {
  try {
    for (const key of draftKeys(window.sessionStorage)) window.sessionStorage.removeItem(key);
  } catch {
    // Blocked storage keeps no draft to forget.
  }
}

/** Forgets the drafts of every member but the one signed in now. */
export function forgetImportDraftsOfOthers(member: string): void {
  try {
    const mine = [projectPrefix, firebasePrefix].map((prefix) => `${prefix}${member}.`);
    for (const key of draftKeys(window.sessionStorage)) {
      if (!mine.some((prefix) => key.startsWith(prefix))) window.sessionStorage.removeItem(key);
    }
  } catch {
    // Blocked storage keeps no draft to forget.
  }
}
