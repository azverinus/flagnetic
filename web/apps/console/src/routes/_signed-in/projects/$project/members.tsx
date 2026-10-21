import { deleteGrant, type Grant, type ProjectMember } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { createFileRoute } from "@tanstack/react-router";
import type { ColumnDef } from "@tanstack/react-table";
import { PlusIcon, Trash2Icon, UserPlusIcon, UsersIcon } from "lucide-react";
import { useContext, useMemo, useState } from "react";

import { Can } from "@/components/can";
import { GrantForm } from "@/components/grant-form";
import { QueryError } from "@/components/query-error";
import { RowAction, RowDialog, withRowActions } from "@/components/row-dialogs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Table } from "@/components/ui/table";
import { AccessContext, useCan } from "@/lib/access";
import { type Permission, roleName } from "@/lib/access-names";
import { unwrap } from "@/lib/errors";
import { memberName, memberStatusName } from "@/lib/members";
import { useListMutation } from "@/lib/mutations";
import { grantOrigin } from "@/lib/project-members";
import { projectsQuery } from "@/lib/queries";
import { useKnownMembers } from "@/lib/use-known-members";

export const Route = createFileRoute("/_signed-in/projects/$project/members")({
  staticData: { crumb: msg`Members` },
  component: Members,
});

const permission: Permission = "grant.manage";

/** No member named yet, while the members who may have made a grant are read. */
const noNames: ReadonlyMap<string, string> = new Map();

/** A grant with the member holding it, as the removal dialog names both. */
type GrantOf = { grant: Grant; member: ProjectMember };

/**
 * Who holds what in the project: every member holding a grant, with each
 * grant side by side, where it comes from, who made it and when.
 */
function Members() {
  const { i18n, t } = useLingui();
  const { project } = Route.useParams();
  const { roles } = useContext(AccessContext);
  const canManage = useCan(permission);
  const {
    projectMembers: members,
    instanceAdmin,
    failure,
    candidates,
    ...known
  } = useKnownMembers(project);
  const names = known.names ?? noNames;
  const [granting, setGranting] = useState(false);
  const [grantingTo, setGrantingTo] = useState<ProjectMember>();
  const [removing, setRemoving] = useState<GrantOf>();

  const columns = useMemo(() => {
    const columns: ColumnDef<ProjectMember, unknown>[] = [
      {
        id: "member",
        header: t`Member`,
        cell: ({ row }) => (
          <span className="flex flex-col">
            <span className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{memberName(row.original)}</span>
              {row.original.status === "active" ? null : (
                <Badge variant="muted">{memberStatusName(i18n, row.original.status)}</Badge>
              )}
            </span>
            <span className="text-xs text-muted-foreground">{row.original.email}</span>
          </span>
        ),
      },
      {
        id: "grants",
        header: t`Grants`,
        cell: ({ row }) => (
          <ul className="flex flex-col gap-2">
            {row.original.grants.map((grant) => {
              const role = roleName(i18n, grant.role);
              const holder = memberName(row.original);
              return (
                <li key={grant.id} className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="min-w-24 font-medium">{role}</span>
                  <span className="text-muted-foreground">{grantOrigin(i18n, names, grant)}</span>
                  {canManage ? (
                    <RowAction
                      label={t`Remove ${role} from ${holder}`}
                      icon={Trash2Icon}
                      onClick={() => setRemoving({ grant, member: row.original })}
                    />
                  ) : null}
                </li>
              );
            })}
          </ul>
        ),
      },
    ];
    return withRowActions(columns, canManage, (row) => {
      const name = memberName(row);
      return (
        <RowAction
          label={t`Grant a role to ${name}`}
          icon={UserPlusIcon}
          onClick={() => setGrantingTo(row)}
        />
      );
    });
  }, [canManage, i18n, names, t]);

  const grantRole = (
    <Can permission={permission} whenMissing="disable">
      <Button onClick={() => setGranting(true)}>
        <PlusIcon aria-hidden />
        <Trans>Grant a role</Trans>
      </Button>
    </Can>
  );
  const grantForm = (member?: string, onDone = () => setGranting(false)) => (
    <GrantForm
      project={project}
      candidates={candidates}
      projectMembersOnly={!instanceAdmin}
      roles={roles}
      member={member}
      onDone={onDone}
    />
  );

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Members`}
        description={
          <Trans>
            The members holding a role in this project. A member may hold several, and can do
            whatever any of them allows.
          </Trans>
        }
        actions={grantRole}
      />
      {failure ? (
        <QueryError error={failure.error} onRetry={failure.retry} />
      ) : members.data?.length === 0 ? (
        <EmptyState
          icon={<UsersIcon />}
          title={<Trans>Nobody holds a role in this project yet.</Trans>}
          description={
            <Trans>Grant members of the instance the roles their work in it needs.</Trans>
          }
          action={grantRole}
        />
      ) : (
        <Table
          caption={t`Members of the project`}
          hideCaption
          columns={columns}
          data={members.data ?? []}
          // Who made each grant is named once every member it may be is read.
          loading={members.isPending || known.names === undefined}
          getRowId={(member) => member.member}
        />
      )}

      <Dialog open={granting} onOpenChange={setGranting} title={t`Grant a role`}>
        {granting ? grantForm() : null}
      </Dialog>
      <RowDialog
        row={grantingTo}
        onClose={() => setGrantingTo(undefined)}
        title={(member) => {
          const name = memberName(member);
          return t`Grant a role to ${name}`;
        }}
      >
        {(member) => grantForm(member.member, () => setGrantingTo(undefined))}
      </RowDialog>
      <RemoveGrantDialog
        project={project}
        removing={removing}
        onDone={() => setRemoving(undefined)}
      />
    </div>
  );
}

/** The confirmation of removing one grant, which leaves the member's others alone. */
function RemoveGrantDialog({
  project,
  removing,
  onDone,
}: {
  project: string;
  removing: GrantOf | undefined;
  onDone: () => void;
}) {
  const { i18n, t } = useLingui();
  const removal = useListMutation(
    (grant: Grant) => unwrap(deleteGrant({ path: { project, grant: grant.id } })),
    // Every read of every project, the projects list among them: the
    // caller's own permissions, and the projects they can open, change
    // when they remove their own grant.
    projectsQuery.queryKey,
    onDone,
  );
  const role = removing ? roleName(i18n, removing.grant.role) : "";
  const holder = removing ? memberName(removing.member) : "";
  const others = (removing?.member.grants.length ?? 0) - 1;
  return (
    <ConfirmDialog
      open={removing !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          removal.reset();
          onDone();
        }
      }}
      title={t`Remove ${role} from ${holder}`}
      confirmLabel={<Trans>Remove grant</Trans>}
      destructive
      pending={removal.isPending}
      refusal={removal.error}
      onConfirm={() => removing && removal.mutate(removing.grant)}
    >
      <p>
        {others > 0 ? (
          <Trans>
            {holder} no longer holds {role} in this project and keeps their other roles here.
          </Trans>
        ) : (
          <Trans>
            {holder} no longer holds any role in this project and leaves its members. They stay a
            member of the instance.
          </Trans>
        )}
      </p>
    </ConfirmDialog>
  );
}
