import { client } from "@flagnetic/api";
import { useQueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useContext, useEffect, useRef, useState } from "react";

import { applyNotice, applyRefetch, type ChangeNotice, parseNotice } from "./change-notices";

type Listener = (notice: ChangeNotice) => void;
type Subscribe = (listener: Listener) => () => void;

const NoticesContext = createContext<Subscribe>(() => () => {});

function openStream(url: string): EventSource {
  return new EventSource(url, { withCredentials: true });
}

/** Where a project's change stream is served. */
export function changeStreamUrl(project: string): string {
  const base = (client.getConfig().baseUrl ?? "").replace(/\/$/, "");
  return `${base}/projects/${encodeURIComponent(project)}/stream`;
}

/**
 * Keeps the console current while a project is open: one stream of the
 * project's changes, whose notices mark stale what they name, so lists and
 * details read themselves again, and which forms listen to
 * (`useChangeNotices`). The browser reconnects a dropped stream by itself,
 * naming the last notice it heard, and the server says `refetch` when it
 * cannot catch the console up from there.
 */
export function ChangeStream({
  project,
  children,
  open = openStream,
}: {
  project: string;
  children: ReactNode;
  /** Opens the stream; the browser's own by default. */
  open?: (url: string) => EventSource;
}) {
  const queryClient = useQueryClient();
  const [listeners] = useState(() => new Set<Listener>());
  const [subscribe] = useState<Subscribe>(() => (listener: Listener) => {
    listeners.add(listener);
    return () => void listeners.delete(listener);
  });

  useEffect(() => {
    const source = open(changeStreamUrl(project));
    source.addEventListener("change", (event) => {
      const notice = parseNotice(event.lastEventId, String(event.data));
      if (!notice) {
        applyRefetch(queryClient, project);
        return;
      }
      applyNotice(queryClient, project, notice);
      for (const listener of listeners) listener(notice);
    });
    source.addEventListener("refetch", () => applyRefetch(queryClient, project));
    return () => source.close();
  }, [project, queryClient, open, listeners]);

  return <NoticesContext value={subscribe}>{children}</NoticesContext>;
}

/** Calls `listener` with each notice of the open project's stream; the latest listener is used. */
export function useChangeNotices(listener: Listener) {
  const subscribe = useContext(NoticesContext);
  const latest = useRef(listener);
  useEffect(() => {
    latest.current = listener;
  });
  useEffect(() => subscribe((notice) => latest.current(notice)), [subscribe]);
}

/** The entity a form edits, as a notice names it. */
export type NoticeSubject = { kind: string; key: string; environment?: string };

/** Whether a notice names the entity, in the environment if the entity lives in one. */
export function noticeNames(notice: ChangeNotice, subject: NoticeSubject): boolean {
  return (
    notice.kind === subject.kind &&
    notice.key === subject.key &&
    (subject.environment === undefined || notice.environment === subject.environment)
  );
}

/**
 * Calls `onNewer` when a notice names the entity a form with unsaved edits
 * is editing, at a version later than the one the form holds (any, when
 * the notice carries none). The form's own save is told apart by its
 * version: its notice names the version just saved.
 */
export function useNewerVersionNotice(
  subject: NoticeSubject,
  { dirty, version }: { dirty: boolean; version: number },
  onNewer: () => void,
) {
  useChangeNotices((notice) => {
    if (!dirty || !noticeNames(notice, subject)) return;
    if (notice.version !== null && notice.version <= version) return;
    onNewer();
  });
}
