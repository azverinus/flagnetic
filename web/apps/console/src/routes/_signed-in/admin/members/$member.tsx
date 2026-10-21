import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { MemberActions } from "@/components/member-actions";
import { MemberSessions, MemberTokens } from "@/components/member-credentials";
import { QueryError } from "@/components/query-error";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { formatDateTime, formatNumber } from "@/i18n/format";
import { ApiError } from "@/lib/errors";
import { memberName, memberStatusName } from "@/lib/members";
import { memberQuery, meQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/admin/members/$member")({
  validateSearch: (search: Record<string, unknown>): { tab?: string } =>
    typeof search.tab === "string" ? { tab: search.tab } : {},
  staticData: { crumb: msg`Member` },
  component: MemberPage,
});

/**
 * One member, for an instance admin: who they are and what they may do,
 * the changes an instance admin makes to them, and the sessions and API
 * tokens they hold, to revoke in an incident.
 */
function MemberPage() {
  const { i18n, t } = useLingui();
  const { member: id } = Route.useParams();
  const member = useQuery(memberQuery(id));
  const { data: me } = useSuspenseQuery(meQuery);

  if (member.data === undefined) {
    if (member.isError) {
      // An address whose identifier is no identifier at all names no
      // member either.
      if (
        member.error instanceof ApiError &&
        (member.error.status === 404 || member.error.status === 400)
      ) {
        return <MemberNotFound />;
      }
      return (
        <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
          <QueryError error={member.error} onRetry={() => void member.refetch()} />
        </div>
      );
    }
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  const shown = member.data;
  const self = me.member?.id === shown.id;
  const name = memberName(shown);

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={name}
        description={shown.email}
        actions={<MemberActions member={shown} self={self} />}
      />
      <dl className="grid grid-cols-1 gap-x-8 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
        <Detail term={<Trans>Status</Trans>}>{memberStatusName(i18n, shown.status)}</Detail>
        <Detail term={<Trans>Instance admin</Trans>}>
          {shown.instance_admin ? t`Yes` : t`No`}
        </Detail>
        <Detail term={<Trans>Projects</Trans>}>{formatNumber(i18n, shown.project_count)}</Detail>
        <Detail term={<Trans>Last sign-in</Trans>}>
          {shown.last_sign_in_at ? (
            <time dateTime={shown.last_sign_in_at}>
              {formatDateTime(i18n, shown.last_sign_in_at)}
            </time>
          ) : (
            t`Never`
          )}
        </Detail>
        <Detail term={<Trans>Member since</Trans>}>
          <time dateTime={shown.created_at}>{formatDateTime(i18n, shown.created_at)}</time>
        </Detail>
      </dl>
      <Tabs
        label={t`What ${name} holds`}
        tabs={[
          {
            value: "sessions",
            label: <Trans>Sessions</Trans>,
            content: <MemberSessions member={shown} self={self} />,
          },
          {
            value: "tokens",
            label: <Trans>API tokens</Trans>,
            content: <MemberTokens member={shown} />,
          },
        ]}
      />
    </div>
  );
}

function MemberNotFound() {
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Member not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>No member has this identifier. The link may be mistyped or old.</Trans>
      </p>
      <Button asChild variant="outline">
        <Link to="/admin/members">
          <Trans>Go to the members</Trans>
        </Link>
      </Button>
    </div>
  );
}

function Detail({ term, children }: { term: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground">{term}</dt>
      <dd className="font-medium">{children}</dd>
    </div>
  );
}
