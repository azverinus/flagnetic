import type { ChangeRequest, ChangeRequestDecision } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

import { ChangeRequestActions } from "@/components/change-request-actions";
import { ChangeRequestDiff } from "@/components/change-request-diff";
import { ChangeRequestStateBadge } from "@/components/change-request-state-badge";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { formatDateTime } from "@/i18n/format";
import { changeRequestKindName, diffBasis } from "@/lib/change-requests";
import { ApiError } from "@/lib/errors";
import { changeRequestQuery, environmentsQuery, flagQuery, segmentQuery } from "@/lib/queries";

export const Route = createFileRoute(
  "/_signed-in/projects/$project/change-requests/$changeRequest",
)({
  staticData: { crumb: msg`Change request` },
  component: ChangeRequestDetail,
});

function ChangeRequestDetail() {
  const { project, changeRequest: id } = Route.useParams();
  const changeRequest = useQuery(changeRequestQuery(project, id));

  if (changeRequest.isError) {
    // An identifier that is no UUID is refused as a bad request: it names
    // no change request either.
    const missing =
      changeRequest.error instanceof ApiError &&
      (changeRequest.error.status === 404 || changeRequest.error.status === 400);
    return missing ? (
      <ChangeRequestNotFound project={project} />
    ) : (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <QueryError error={changeRequest.error} onRetry={() => void changeRequest.refetch()} />
      </div>
    );
  }
  if (changeRequest.data === undefined) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  return <ChangeRequestPage project={project} changeRequest={changeRequest.data} />;
}

function ChangeRequestNotFound({ project }: { project: string }) {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Change request not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>
          The project has no such change request. It goes when the flag or the environment it
          proposes for is deleted.
        </Trans>
      </p>
      <Button asChild variant="outline">
        <Link to="/projects/$project/change-requests" params={{ project }}>
          <Trans>Go to the change requests</Trans>
        </Link>
      </Button>
    </div>
  );
}

function Fact({ term, children }: { term: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground">{term}</dt>
      <dd>{children}</dd>
    </div>
  );
}

function Moment({ at }: { at: string }) {
  const { i18n } = useLingui();
  return <time dateTime={at}>{formatDateTime(i18n, at)}</time>;
}

/**
 * One change request: what it proposes for which flag and environment,
 * who wrote and decided it, what it would change, and the actions the
 * caller may take on it.
 */
function ChangeRequestPage({
  project,
  changeRequest,
}: {
  project: string;
  changeRequest: ChangeRequest;
}) {
  const { i18n, t } = useLingui();
  const flag = useQuery({
    ...flagQuery(project, changeRequest.flag ?? ""),
    enabled: changeRequest.flag !== undefined,
  });
  const segment = useQuery({
    ...segmentQuery(project, changeRequest.segment ?? ""),
    enabled: changeRequest.segment !== undefined,
  });
  const environments = useQuery(environmentsQuery(project));
  const environmentName = (key: string) =>
    environments.data?.find((environment) => environment.key === key)?.name ?? key;
  // What the change request would change as it is now: the segment of a
  // segment's definition, the flag of a flag's, otherwise the
  // environment's configuration.
  const subject = changeRequest.segment !== undefined ? segment : flag;
  const current =
    changeRequest.segment !== undefined
      ? segment.data
      : changeRequest.kind === "definition"
        ? flag.data
        : flag.data?.environments.find(
            (configuration) => configuration.environment === changeRequest.environment,
          );
  const { state, author, approval, rejection, applied_at, applied_by } = changeRequest;
  const waiting = state === "draft" || state === "open" || state === "approved";
  const baseMoved =
    waiting && current !== undefined && diffBasis(changeRequest, current) === "patch";
  const baseVersion = changeRequest.base_version;
  const authorName = author.name;

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={changeRequest.title}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <ChangeRequestStateBadge state={state} />
            {changeRequestKindName(i18n, changeRequest.kind)}
          </span>
        }
        actions={<ChangeRequestActions project={project} changeRequest={changeRequest} />}
      />

      {baseMoved ? (
        <p
          role="status"
          className="flex items-start gap-2 rounded-md border bg-muted/40 p-3 text-sm"
        >
          <TriangleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
          {state === "draft" ? (
            <Trans>
              What this change request was based on changed after it was written. Rebase it onto
              the current version before opening it.
            </Trans>
          ) : (
            <Trans>
              What this change request was based on changed after it was written; it can no longer
              be approved or applied. Copy it to propose it again.
            </Trans>
          )}
        </p>
      ) : null}
      {state === "superseded" ? (
        <p role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          <Trans>
            Superseded: what this change request was based on changed, so it can no longer be
            applied. Copy it to propose it again on the current configuration.
          </Trans>
        </p>
      ) : null}

      <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-3">
        {changeRequest.flag ? (
          <Fact term={<Trans>Flag</Trans>}>
            <Link
              to="/projects/$project/flags/$flag"
              params={{ project, flag: changeRequest.flag }}
              className="font-mono text-xs hover:underline"
            >
              {changeRequest.flag}
            </Link>
          </Fact>
        ) : null}
        {changeRequest.segment ? (
          <Fact term={<Trans>Segment</Trans>}>
            <Link
              to="/projects/$project/segments/$segment"
              params={{ project, segment: changeRequest.segment }}
              className="font-mono text-xs hover:underline"
            >
              {changeRequest.segment}
            </Link>
          </Fact>
        ) : null}
        {changeRequest.environment ? (
          <Fact term={<Trans>Environment</Trans>}>
            <Link
              to="/projects/$project/environments/$environment"
              params={{ project, environment: changeRequest.environment }}
              className="hover:underline"
            >
              {environmentName(changeRequest.environment)}
            </Link>
          </Fact>
        ) : null}
        <Fact term={<Trans>Based on</Trans>}>
          <Trans>Version {baseVersion}</Trans>
        </Fact>
        <Fact term={<Trans>Author</Trans>}>
          {author.kind === "token" ? <Trans>{authorName} (project token)</Trans> : authorName}
        </Fact>
        <Fact term={<Trans>Created</Trans>}>
          <Moment at={changeRequest.created_at} />
        </Fact>
        {applied_at !== null ? (
          <Fact term={<Trans>Applied</Trans>}>
            <Moment at={applied_at} />
            {applied_by ? (
              <>
                {" · "}
                {applied_by.kind === "system" ? t`at its scheduled time` : applied_by.name}
              </>
            ) : null}
          </Fact>
        ) : null}
        {changeRequest.description ? (
          <div className="flex flex-col gap-0.5 sm:col-span-3">
            <dt className="text-muted-foreground">
              <Trans>Description</Trans>
            </dt>
            <dd className="whitespace-pre-wrap">{changeRequest.description}</dd>
          </div>
        ) : null}
      </dl>

      {approval || rejection ? (
        <section aria-labelledby="decisions" className="flex flex-col gap-2">
          <h2 id="decisions" className="text-base font-semibold">
            <Trans>Decision</Trans>
          </h2>
          {approval ? <Decision decision={approval} approved /> : null}
          {rejection ? <Decision decision={rejection} approved={false} /> : null}
        </section>
      ) : null}

      <section aria-labelledby="changes" className="flex flex-col gap-2">
        <h2 id="changes" className="text-base font-semibold">
          <Trans>Changes</Trans>
        </h2>
        {subject.isError ? (
          <QueryError error={subject.error} onRetry={() => void subject.refetch()} />
        ) : subject.isPending && subject.fetchStatus !== "idle" ? (
          <Skeleton className="h-16 w-full" />
        ) : (
          <ChangeRequestDiff changeRequest={changeRequest} current={current} />
        )}
      </section>
    </div>
  );
}

function Decision({ decision, approved }: { decision: ChangeRequestDecision; approved: boolean }) {
  const name = decision.member.name;
  return (
    <div className="flex flex-col gap-1 rounded-md border p-3 text-sm">
      <p>
        {approved ? <Trans>Approved by {name}</Trans> : <Trans>Rejected by {name}</Trans>}
        {" · "}
        <Moment at={decision.at} />
      </p>
      {decision.comment ? (
        <blockquote className="border-l-2 pl-3 whitespace-pre-wrap text-muted-foreground">
          {decision.comment}
        </blockquote>
      ) : null}
    </div>
  );
}
