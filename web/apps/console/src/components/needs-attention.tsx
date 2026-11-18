import { plural } from "@lingui/core/macro";
import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ChevronRightIcon } from "lucide-react";
import { type ReactNode, useId } from "react";

import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { useCan } from "@/lib/access";
import { awaitingCountLimit, awaitingDecisionQuery, staleFlagsQuery } from "@/lib/queries";
import { needsAttention, sinksQuery } from "@/lib/sinks";
import { isPlatformDisabled, webhooksQuery } from "@/lib/webhooks";

const linkClassName =
  "flex items-center justify-between gap-3 rounded-md px-3 py-2 text-sm hover:bg-accent focus-visible:outline-2 focus-visible:outline-ring";

/** What a row is: its link, with the sentence and a chevron saying it goes somewhere. */
function RowText({ text }: { text: string }) {
  return (
    <>
      <span>{text}</span>
      <ChevronRightIcon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
    </>
  );
}

/**
 * What waits on the member in the project, each as a row linking to the
 * screen that deals with it: change requests awaiting their decision,
 * stale flags, sinks that need attention and webhooks the platform
 * disabled. Each reads the list its own screen reads, and only for a
 * member holding the permission that screen needs. A row with nothing to
 * say, or whose list could not be read, is absent, and so is the card
 * when no row is left: attention that is not there is not worth a box.
 */
export function NeedsAttention({ project }: { project: string }) {
  const { t } = useLingui();
  const heading = useId();
  const decides = useCan("change_request.approve");
  const managesSinks = useCan("sink.manage");
  const managesWebhooks = useCan("webhook.manage");
  const awaiting = useQuery({ ...awaitingDecisionQuery(project), enabled: decides });
  const stale = useQuery(staleFlagsQuery(project, {}));
  const sinks = useQuery({ ...sinksQuery(project), enabled: managesSinks });
  const webhooks = useQuery({ ...webhooksQuery({ project }), enabled: managesWebhooks });

  const rows: ReactNode[] = [];

  const count = awaiting.data?.items.length ?? 0;
  if (decides && count > 0) {
    const more = count > awaitingCountLimit;
    rows.push(
      <li key="awaiting">
        <Link
          to="/projects/$project/change-requests"
          params={{ project }}
          search={{ tab: "awaiting" }}
          className={linkClassName}
        >
          <RowText
            text={
              more
                ? t`More than ${awaitingCountLimit} change requests await your decision`
                : t`${plural(count, {
                    one: "# change request awaits your decision",
                    other: "# change requests await your decision",
                  })}`
            }
          />
        </Link>
      </li>,
    );
  }

  const staleCount = stale.data?.items.length ?? 0;
  if (staleCount > 0) {
    const more = stale.data?.next_cursor != null;
    rows.push(
      <li key="stale">
        <Link to="/projects/$project/stale-flags" params={{ project }} className={linkClassName}>
          <RowText
            text={
              more
                ? t`${staleCount}+ stale flags may be ready to remove`
                : t`${plural(staleCount, {
                    one: "# stale flag may be ready to remove",
                    other: "# stale flags may be ready to remove",
                  })}`
            }
          />
        </Link>
      </li>,
    );
  }

  const sinkCount = (sinks.data ?? []).filter(needsAttention).length;
  if (managesSinks && sinkCount > 0) {
    rows.push(
      <li key="sinks">
        <Link to="/projects/$project/sinks" params={{ project }} className={linkClassName}>
          <RowText
            text={t`${plural(sinkCount, {
              one: "# sink needs attention",
              other: "# sinks need attention",
            })}`}
          />
        </Link>
      </li>,
    );
  }

  const webhookCount = (webhooks.data ?? []).filter(isPlatformDisabled).length;
  if (managesWebhooks && webhookCount > 0) {
    rows.push(
      <li key="webhooks">
        <Link to="/projects/$project/webhooks" params={{ project }} className={linkClassName}>
          <RowText
            text={t`${plural(webhookCount, {
              one: "# webhook disabled by the platform",
              other: "# webhooks disabled by the platform",
            })}`}
          />
        </Link>
      </li>,
    );
  }

  if (rows.length === 0) return null;
  return (
    <Card aria-labelledby={heading}>
      <CardHeader title={t`Needs attention`} titleId={heading} />
      <CardContent className="pt-3">
        <ul aria-label={t`Needs attention`} className="-mx-3 flex flex-col">
          {rows}
        </ul>
      </CardContent>
    </Card>
  );
}
