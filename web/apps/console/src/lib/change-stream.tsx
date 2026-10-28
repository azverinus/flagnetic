import { client } from "@flagnetic/api";
import { useQueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useContext, useEffect, useRef, useState } from "react";

import { applyNotice, applyRefetch, type ChangeNotice, parseNotice } from "./change-notices";

type Listener = (notice: ChangeNotice) => void;
type Subscribe = (listener: Listener) => () => void;

const NoticesContext = createContext<Subscribe>(() => () => {});

/** `EventSource.CLOSED`, which a test's stand-in for the browser's own may lack. */
const closed = 2;
const firstDelay = 1_000;
const lastDelay = 60_000;

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
 * cannot catch the console up from there. A stream the browser gave up on
 * is opened again here.
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
    let source: EventSource;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let delay = firstDelay;
    // A stream opened again after the browser gave up names no last
    // notice, so whatever changed meanwhile is read again in full.
    let missed = false;
    const connect = () => {
      source = open(changeStreamUrl(project));
      source.addEventListener("open", () => {
        delay = firstDelay;
        if (missed) applyRefetch(queryClient, project);
        missed = false;
      });
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
      // The browser reconnects by itself after a dropped connection, but
      // gives up for good when an attempt is answered with anything but a
      // stream (too many streams, a proxy's error page while the server
      // restarts): then the stream is opened again, ever more slowly.
      source.addEventListener("error", () => {
        if (source.readyState !== closed) return;
        source.close();
        missed = true;
        retry = setTimeout(connect, delay);
        delay = Math.min(delay * 2, lastDelay);
      });
    };
    connect();
    return () => {
      clearTimeout(retry);
      source.close();
    };
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

/**
 * The entity a form edits, as a notice names it: in one environment, in
 * none (`null`, as a flag's definition), or in any when left out.
 */
export type NoticeSubject = { kind: string; key: string; environment?: string | null };

/** Whether a notice names the entity, in the environment if the entity lives in one. */
export function noticeNames(notice: ChangeNotice, subject: NoticeSubject): boolean {
  return (
    notice.kind === subject.kind &&
    notice.key === subject.key &&
    (subject.environment === undefined || notice.environment === subject.environment)
  );
}

/** Whether a version read after a notice is still newer than what the form holds now. */
export type StillNewer = (version: number) => boolean;

/**
 * Calls `onNewer` when a notice names the entity a form with unsaved edits
 * is editing, at a version later than the one the form holds (any, when
 * the notice carries none). The form's own save is told apart by its
 * version: its notice names the version just saved. That notice may come
 * before the save's answer, so none is heeded while the form is saving,
 * and `onNewer` is handed `stillNewer` to ask, once its read is back,
 * whether the form has not moved on to that version meanwhile.
 */
export function useNewerVersionNotice(
  subject: NoticeSubject,
  state: { dirty: boolean; version: number; saving?: boolean },
  onNewer: (stillNewer: StillNewer) => void,
) {
  const latest = useRef(state);
  useEffect(() => {
    latest.current = state;
  });
  const stillNewer: StillNewer = (version) =>
    latest.current.dirty && !latest.current.saving && version > latest.current.version;
  useChangeNotices((notice) => {
    if (!state.dirty || state.saving || !noticeNames(notice, subject)) return;
    if (notice.version !== null && notice.version <= state.version) return;
    onNewer(stillNewer);
  });
}
