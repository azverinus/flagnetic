import type { AuditEntry } from "@flagnetic/api";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import { ApiError } from "./errors";
import { entityEntriesQuery } from "./queries";

/** A save met a newer version: that version, and the entry that made it. */
export type FormConflict<Entity> = { current: Entity; entry: AuditEntry | undefined };

export type FormConflictOptions<Entity, Initial> = {
  /**
   * The project whose audit log names who made a newer version; none for
   * an entity of the instance, whose banner then names no one.
   */
  project: string | undefined;
  /** The audit log's name for the entity's kind, such as `environment`. */
  kind: string;
  /**
   * The entity as the form was filled from it; none for a form that
   * creates one, which never meets a newer version.
   */
  initial: Initial;
  /** Reads the entity as it is now, past any cache. */
  read: () => Promise<Entity>;
};

/**
 * The state of a form of a project, an environment, a unit type or an
 * attribute that saves under `If-Match` and meets versions someone else
 * saved meanwhile.
 *
 * It keeps apart the entity the form was filled from (`base`), which a
 * save compares the values with to send only what the member changed, and
 * the version a save names (`version`): keeping one's edits over a newer
 * version saves onto that version what was changed from the older one, so
 * the other change survives in every field the member left alone. Both
 * are kept apart from the query cache too, since a refetch bringing
 * someone else's change must never make a save overwrite it unseen.
 *
 * A refusal for a version mismatch reads the newer version and who saved
 * it, for the shared conflict banner; when it cannot be read, the
 * refusal's own message stays, which says to reload.
 */
export function useFormConflict<
  Entity extends { id: string; version: number },
  Initial extends Entity | undefined = Entity,
>({ project, kind, initial, read }: FormConflictOptions<Entity, Initial>) {
  const queryClient = useQueryClient();
  const [base, setBase] = useState<Initial | Entity>(initial);
  const [version, setVersion] = useState<number | undefined>(initial?.version);
  const [conflict, setConflict] = useState<FormConflict<Entity>>();
  const [unread, setUnread] = useState(false);
  // Counts the conflicts cleared, so a read still under way when the
  // member moved on (reloading, or saving again) shows nothing.
  const generation = useRef(0);

  /** Forgets the conflict shown, and any read of one still under way. */
  const clear = () => {
    generation.current += 1;
    setConflict(undefined);
    setUnread(false);
  };

  /** The entry that made the version just read: none for an entity outside a project. */
  const entryOf = async (current: Entity) => {
    if (project === undefined) return undefined;
    // The newest entry of the entity made the version just read: these
    // entries record no version to match it by.
    const entries = await queryClient
      .fetchQuery({ ...entityEntriesQuery(project, kind, current.id, 1), staleTime: 0 })
      .catch(() => [] as AuditEntry[]);
    return entries[0];
  };

  /** Reads what the banner shows, when the refusal is a version mismatch. */
  const meet = async (error: unknown) => {
    if (!(error instanceof ApiError) || error.code !== "version_mismatch") return;
    const at = generation.current;
    let current: Entity;
    try {
      current = await read();
    } catch {
      if (at === generation.current) setUnread(true);
      return;
    }
    const entry = await entryOf(current);
    if (at === generation.current) setConflict({ current, entry });
  };

  /**
   * Reads the entity after a notice of a change by someone else, and shows
   * the banner when it is newer than the version the form names. A read
   * that fails shows nothing: Save meets the refusal itself. A form that
   * moved on while reading (saved, reloaded) shows nothing either.
   */
  const meetNewer = async (
    stillNewer: (version: number) => boolean = (newer) => version === undefined || newer > version,
  ) => {
    if (conflict) return;
    const at = generation.current;
    let current: Entity;
    try {
      current = await read();
    } catch {
      return;
    }
    if (!stillNewer(current.version)) return;
    const entry = await entryOf(current);
    if (at === generation.current && stillNewer(current.version)) {
      setConflict({ current, entry });
    }
  };

  /**
   * Shows the banner for a newer version the screen already holds (a
   * refetch or a write elsewhere on the screen answered it), without
   * reading it again. A form that moved on meanwhile shows nothing.
   */
  const offer = async (current: Entity) => {
    if (conflict && conflict.current.version >= current.version) return;
    const at = generation.current;
    const entry = await entryOf(current);
    if (at === generation.current) setConflict({ current, entry });
  };

  /** Starts the form over from an entity: one just saved, or the newer version. */
  const restart = (from: Entity) => {
    setBase(from);
    setVersion(from.version);
    clear();
  };

  /**
   * Keeps the member's edits over the newer version: later saves name it,
   * while `base` stays what the edits were made from. Returns the version.
   */
  const keep = (current: Entity) => {
    setVersion(current.version);
    clear();
    return current.version;
  };

  /**
   * The refusal a form shows in its usual place: none for a version
   * mismatch, which the banner explains once it is read, unless it could
   * not be read.
   */
  const refusal = (error: unknown) =>
    error instanceof ApiError && error.code === "version_mismatch" && !unread ? undefined : error;

  return {
    base,
    // A version exactly when there is an entity it is the version of.
    version: version as Initial extends Entity ? number : number | undefined,
    conflict,
    meet,
    meetNewer,
    offer,
    clear,
    restart,
    keep,
    refusal,
  };
}

/**
 * Hands a form the newer version of its entity that the screen came to
 * hold (the query refetched, or a write elsewhere on the screen answered
 * it): a clean form starts over from it, a form with unsaved edits is
 * offered the conflict instead of losing them. A form is keyed by its
 * entity's id, never by its version, so this is the only way a newer
 * version reaches it.
 */
export function useNewerArrival<Entity extends { version: number }>(
  current: Entity | undefined,
  state: { version: number | undefined; dirty: boolean; saving?: boolean },
  on: { clean: (current: Entity) => void; dirty: (current: Entity) => void },
) {
  const latest = useRef({ state, on });
  useEffect(() => {
    latest.current = { state, on };
  });
  useEffect(() => {
    const { state: held, on: handlers } = latest.current;
    if (!current || held.version === undefined || held.saving) return;
    if (current.version <= held.version) return;
    if (held.dirty) handlers.dirty(current);
    else handlers.clean(current);
  }, [current]);
}
