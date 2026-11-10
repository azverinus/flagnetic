import {
  inviteMember,
  type Member,
  type MemberInvitation,
  type MemberInvite,
  type MemberStatus,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { UserPlusIcon } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";

import { InvitationLinkDialog } from "@/components/invitation-link-dialog";
import { InviteForm, type InviteProjects, type InviteValues } from "@/components/invite-form";
import { QueryError } from "@/components/query-error";
import { SearchField } from "@/components/search-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field } from "@/components/ui/field";
import { PageHeader } from "@/components/ui/page-header";
import { Select, SelectItem } from "@/components/ui/select";
import { Table, type TableColumn } from "@/components/ui/table";
import { formatDateTime, formatNumber } from "@/i18n/format";
import { unwrap } from "@/lib/errors";
import {
  type MemberSearch,
  memberName,
  memberStatuses,
  memberStatusName,
  validateMemberSearch,
} from "@/lib/members";
import {
  memberSearchQuery,
  projectInvitationsQuery,
  projectsQuery,
  rolesQuery,
} from "@/lib/queries";
import { forgetsSecret } from "@/lib/mutations";

export const Route = createFileRoute("/_signed-in/admin/members/")({
  validateSearch: validateMemberSearch,
  component: Members,
});

/** The status filter's value for every status. */
const everyStatus = "all";

/**
 * Every member of the instance, found by name or email and filtered by
 * status, with inviting someone: the invitation is mailed, or its one-time
 * link is shown once, to pass on.
 */
function Members() {
  const { i18n, t } = useLingui();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const members = useQuery(memberSearchQuery(search));
  const [inviting, setInviting] = useState(false);
  const [issued, setIssued] = useState<MemberInvitation>();
  const filtered = search.q !== undefined || search.status !== undefined;

  const apply = (next: MemberSearch) => void navigate({ search: next, replace: true });

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Members`}
        description={
          <Trans>
            Everyone who can sign in to this instance. What they may do in a project comes from
            their grants there.
          </Trans>
        }
        actions={
          <Button onClick={() => setInviting(true)}>
            <UserPlusIcon aria-hidden />
            <Trans>Invite member</Trans>
          </Button>
        }
      />
      <div className="flex flex-wrap items-end gap-4">
        <SearchField
          value={search.q ?? ""}
          onChange={(q) => apply({ ...search, q: q.trim() === "" ? undefined : q.trim() })}
        />
        <Field label={<Trans>Status</Trans>} className="w-48">
          <Select
            value={search.status ?? everyStatus}
            onValueChange={(value) =>
              apply({
                ...search,
                status: value === everyStatus ? undefined : (value as MemberStatus),
              })
            }
          >
            <SelectItem value={everyStatus}>{t`Every status`}</SelectItem>
            {memberStatuses.map((status) => (
              <SelectItem key={status} value={status}>
                {memberStatusName(i18n, status)}
              </SelectItem>
            ))}
          </Select>
        </Field>
      </div>
      {members.isError ? (
        <QueryError error={members.error} onRetry={() => void members.refetch()} />
      ) : (
        <MembersTable
          members={members.data ?? []}
          loading={members.isPending}
          empty={
            filtered ? (
              <Trans>No member matches the search and the status filter.</Trans>
            ) : (
              <Trans>No members yet.</Trans>
            )
          }
        />
      )}

      <InviteDialog
        open={inviting}
        onClose={() => setInviting(false)}
        onInvited={(answer) => {
          setInviting(false);
          setIssued(answer);
        }}
      />
      <InvitationLinkDialog issued={issued} kind="invited" onClose={() => setIssued(undefined)} />
    </div>
  );
}

function MembersTable({
  members,
  loading,
  empty,
}: {
  members: Member[];
  loading: boolean;
  empty: ReactNode;
}) {
  const { i18n, t } = useLingui();
  const columns = useMemo<TableColumn<Member>[]>(
    () => [
      {
        id: "member",
        header: t`Member`,
        cell: ({ row }) => {
          const { provider } = row.original;
          return (
            <span className="flex flex-col">
              <span className="flex flex-wrap items-center gap-2">
                <Link
                  to="/admin/members/$member"
                  params={{ member: row.original.id }}
                  className="font-medium hover:underline"
                >
                  {memberName(row.original)}
                </Link>
                {row.original.instance_admin ? (
                  <Badge>
                    <Trans>Instance admin</Trans>
                  </Badge>
                ) : null}
                {provider === null ? null : (
                  <Badge variant="muted">
                    <Trans>Provisioned by {provider}</Trans>
                  </Badge>
                )}
              </span>
              <span className="text-xs text-muted-foreground">{row.original.email}</span>
            </span>
          );
        },
      },
      {
        id: "status",
        header: t`Status`,
        cell: ({ row }) => memberStatusName(i18n, row.original.status),
      },
      {
        id: "projects",
        header: t`Projects`,
        cell: ({ row }) => formatNumber(i18n, row.original.project_count),
      },
      {
        id: "signed-in",
        header: t`Last sign-in`,
        cell: ({ row }) =>
          row.original.last_sign_in_at ? (
            <time dateTime={row.original.last_sign_in_at}>
              {formatDateTime(i18n, row.original.last_sign_in_at)}
            </time>
          ) : (
            t`Never`
          ),
      },
    ],
    [i18n, t],
  );
  return (
    <Table
      caption={t`Members`}
      hideCaption
      stickyFirstColumn
      columns={columns}
      data={members}
      loading={loading}
      empty={empty}
      getRowId={(member) => member.id}
    />
  );
}

/**
 * Inviting someone by email and name, with a role in a project if the
 * instance admin chooses one; the answer says the invitation was mailed or
 * carries its one-time link.
 */
function InviteDialog({
  open,
  onClose,
  onInvited,
}: {
  open: boolean;
  onClose: () => void;
  onInvited: (answer: MemberInvitation) => void;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const roles = useQuery({ ...rolesQuery, enabled: open });
  const projects = useQuery({ ...projectsQuery, enabled: open });
  const invite = useMutation({
    mutationFn: (body: MemberInvite) => unwrap(inviteMember({ body })),
    ...forgetsSecret,
    meta: { toast: false },
    onSuccess: async (answer, body) => {
      await queryClient.invalidateQueries({ queryKey: ["members"] });
      // An invitation granting a role lists its member among the project's invited members.
      if (body.grant) {
        await queryClient.invalidateQueries({
          queryKey: projectInvitationsQuery(body.grant.project).queryKey,
        });
      }
      // The link lives in the dialog that shows it, never in the
      // mutation's state: reset detaches the mutation, and with no cache
      // time the mutation cache drops it, answer included, so the link is
      // gone once that dialog closes.
      invite.reset();
      onInvited(answer);
    },
  });
  const close = () => {
    invite.reset();
    onClose();
  };
  // A project is chosen only once both the projects and the roles to
  // grant there are read: a project without roles to choose from would
  // leave the form asking for a role it cannot offer.
  const offered: InviteProjects = projects.isError
    ? { status: "error", error: projects.error, retry: () => void projects.refetch() }
    : roles.isError
      ? { status: "error", error: roles.error, retry: () => void roles.refetch() }
      : projects.data === undefined || roles.data === undefined
        ? { status: "pending" }
        : { status: "success", projects: projects.data };
  const submit = ({ email, display_name, project, role }: InviteValues) =>
    invite.mutate(
      project === "" ? { email, display_name } : { email, display_name, grant: { project, role } },
    );
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !next && close()}
      title={t`Invite a member`}
      description={
        <Trans>
          They get a link to set their password, mailed to them when the instance can send mail and
          shown to you once otherwise. A role in a project, if you choose one, is granted when they
          accept.
        </Trans>
      }
    >
      {open ? (
        <InviteForm
          roles={roles.data ?? []}
          projects={offered}
          pending={invite.isPending}
          refusal={invite.error}
          onCancel={close}
          onSubmit={submit}
        />
      ) : null}
    </Dialog>
  );
}
