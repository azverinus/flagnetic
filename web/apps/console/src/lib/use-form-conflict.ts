import type { AuditEntry } from "@flagnetic/api";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ApiError } from "./errors";
import { entityEntriesQuery } from "./queries";

/** A save met a newer version: that version, and the entry that made it. */
export type FormConflict<Entity> = { current: Entity; entry: AuditEntry | undefined };

export type FormConflictOptions<Entity> = {
  project: string;
  /** The audit log's name for the entity's kind, such as `environment`. */
  kind: string;
  /** Reads the entity as it is now, past any cache. */
  read: () => Promise<Entity>;
};

/**
 * What a form of a project, an environment, a unit type or an attribute
 * shows when its save meets a version someone else saved meanwhile: that
 * version and who saved it, for the shared conflict banner. When the newer
 * version cannot be read, the refusal's own message stays, which says to
 * reload.
 */
export function useFormConflict<Entity extends { id: string }>({
  project,
  kind,
  read,
}: FormConflictOptions<Entity>) {
  const queryClient = useQueryClient();
  const [conflict, setConflict] = useState<FormConflict<Entity>>();
  const [unread, setUnread] = useState(false);

  /** Reads what the banner shows, when the refusal is a version mismatch. */
  const meet = async (error: unknown) => {
    if (!(error instanceof ApiError) || error.code !== "version_mismatch") return;
    let current: Entity;
    try {
      current = await read();
    } catch {
      setUnread(true);
      return;
    }
    // The newest entry of the entity made the version just read: these
    // entries record no version to match it by.
    const entries = await queryClient
      .fetchQuery({ ...entityEntriesQuery(project, kind, current.id, 1), staleTime: 0 })
      .catch(() => [] as AuditEntry[]);
    setConflict({ current, entry: entries[0] });
  };

  const clear = () => {
    setConflict(undefined);
    setUnread(false);
  };

  /**
   * The refusal a form shows in its usual place: none for a version
   * mismatch, which the banner explains once it is read, unless it could
   * not be read.
   */
  const refusal = (error: unknown) =>
    error instanceof ApiError && error.code === "version_mismatch" && !unread ? undefined : error;

  return { conflict, meet, clear, refusal };
}
