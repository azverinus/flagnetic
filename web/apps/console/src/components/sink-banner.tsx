import { plural } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { TriangleAlertIcon } from "lucide-react";

import { useCan } from "@/lib/access";
import { attentionReasons, needsAttention, sinksQuery } from "@/lib/sinks";

/**
 * The sinks of a project that are stopped, at risk of losing events or
 * hold a gap nobody dismissed. They are read again when the change
 * stream tells of a sink, and every minute besides, since a sink also
 * comes to risk by the clock alone.
 */
function useSinksNeedingAttention(project: string, enabled: boolean) {
  const sinks = useQuery({ ...sinksQuery(project), enabled, refetchInterval: 60_000 });
  return (sinks.data ?? []).filter(needsAttention);
}

/**
 * The banner above a project's screens for those who manage its sinks,
 * naming each sink that needs them and why. Others see nothing, and read
 * nothing: the list is not theirs to ask for.
 */
export function ProjectSinkBanner({ project }: { project: string }) {
  const can = useCan("sink.manage");
  const { i18n, t } = useLingui();
  const attention = useSinksNeedingAttention(project, can);
  if (!can || attention.length === 0) return null;
  return (
    <div className="mx-auto w-full max-w-6xl px-6 pt-6">
      <div
        role="status"
        className="flex items-start gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm"
      >
        <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <p className="font-medium">
            {t`${plural(attention.length, {
              one: "# sink needs your attention.",
              other: "# sinks need your attention.",
            })}`}
          </p>
          <ul className="flex flex-col gap-1">
            {attention.map((sink) => (
              <li key={sink.id}>
                <Link
                  to="/projects/$project/sinks/$sink"
                  params={{ project, sink: sink.id }}
                  className="font-medium underline-offset-4 hover:underline"
                >
                  {sink.name}
                </Link>
                <span className="text-muted-foreground">
                  {" "}
                  — {attentionReasons(i18n, sink).join(", ")}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}

/**
 * The dot beside the Sinks item of the navigation while a sink needs
 * attention, said to a screen reader too.
 */
export function SinkAttentionMarker({ project }: { project: string }) {
  const { t } = useLingui();
  const attention = useSinksNeedingAttention(project, true);
  if (attention.length === 0) return null;
  const label = t`${plural(attention.length, {
    one: "# sink needs your attention",
    other: "# sinks need your attention",
  })}`;
  return (
    <span className="ml-auto size-2 shrink-0 rounded-full bg-destructive" title={label}>
      <span className="sr-only">{label}</span>
    </span>
  );
}
