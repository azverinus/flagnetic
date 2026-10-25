import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { GitPullRequestArrowIcon } from "lucide-react";
import { useId } from "react";

import { ChangeRequestStateBadge } from "@/components/change-request-state-badge";
import { waitingByEnvironment } from "@/lib/change-requests";
import { flagWaitingQuery } from "@/lib/queries";

/**
 * The open and approved change requests proposed for a flag's
 * configuration in one environment, so whoever edits it knows a change
 * is pending there; nothing when none waits, or until they are read.
 */
export function WaitingChangeRequests({
  project,
  flag,
  environment,
}: {
  project: string;
  flag: string;
  environment: string;
}) {
  const { t } = useLingui();
  const heading = useId();
  const waiting = useQuery(flagWaitingQuery(project, flag));
  const here = waiting.data ? waitingByEnvironment(waiting.data).get(environment) : undefined;
  if (here === undefined) return null;
  const items = [...here.approved, ...here.open];
  const open = here.open.length;
  const approved = here.approved.length;
  return (
    <section
      aria-labelledby={heading}
      className="flex flex-col gap-2 rounded-md border bg-muted/40 p-3 text-sm"
    >
      <h2 id={heading} className="flex items-center gap-2 font-medium">
        <GitPullRequestArrowIcon aria-hidden className="size-4 shrink-0" />
        <Trans>
          Change requests waiting here: {open} open, {approved} approved
        </Trans>
      </h2>
      <ul aria-label={t`Waiting change requests`} className="flex flex-col gap-1">
        {items.map((changeRequest) => (
          <li key={changeRequest.id} className="flex flex-wrap items-center gap-2">
            <ChangeRequestStateBadge state={changeRequest.state} />
            <Link
              to="/projects/$project/change-requests/$changeRequest"
              params={{ project, changeRequest: changeRequest.id }}
              className="font-medium hover:underline"
            >
              {changeRequest.title}
            </Link>
            <span className="text-muted-foreground">{changeRequest.author.name}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}
