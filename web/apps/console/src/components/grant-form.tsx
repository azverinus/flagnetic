import { createGrant, type Role } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

import { QueryError } from "@/components/query-error";
import { RoleField } from "@/components/role-field";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Select, SelectItem } from "@/components/ui/select";
import { unwrap } from "@/lib/errors";
import { memberName, memberStatusName } from "@/lib/members";
import { useListMutation } from "@/lib/mutations";
import { formRefusal } from "@/lib/problem-messages";
import { projectsQuery } from "@/lib/queries";
import type { GrantCandidates } from "@/lib/project-members";

export type GrantFormProps = {
  project: string;
  /** The members the role can be granted to, as far as they are read. */
  candidates: GrantCandidates;
  /**
   * Whether the candidates are only the project's own members, because
   * the caller cannot read every member of the instance.
   */
  projectMembersOnly: boolean;
  /** Every role, in the server's order. */
  roles: readonly Role[];
  /** The member the dialog was opened for, chosen already. */
  member?: string;
  onDone: () => void;
};

/**
 * Grants one role to one member of the instance in a project. Nothing is
 * chosen first, so a role is always picked on purpose.
 */
export function GrantForm({
  project,
  candidates,
  projectMembersOnly,
  roles,
  member,
  onDone,
}: GrantFormProps) {
  const { i18n, t } = useLingui();
  const schema = z.object({
    member: z.string().min(1, t`Choose a member.`),
    role: z.string().min(1, t`Choose a role.`),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { member: member ?? "", role: "" },
  });
  const grant = useListMutation(
    (values: Values) => unwrap(createGrant({ path: { project }, body: values })),
    // Every read of every project, the projects list among them: the
    // caller's own permissions, and the projects they can open, change
    // when they grant themself a role.
    projectsQuery.queryKey,
    onDone,
  );
  const { errors } = form.formState;
  const offered = candidates.status === "success" ? candidates.members : [];

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) => grant.mutate(values))}
      onCancel={onDone}
      submitLabel={<Trans>Grant role</Trans>}
      pending={grant.isPending}
      refusal={formRefusal(grant.error, ["/member", "/role"])}
    >
      <Controller
        control={form.control}
        name="member"
        render={({ field }) => (
          <Field
            label={<Trans>Member</Trans>}
            hint={
              projectMembersOnly ? (
                <Trans>
                  You can grant further roles to the members of this project. An instance admin
                  grants a first role to anyone else.
                </Trans>
              ) : undefined
            }
            error={errors.member?.message}
            refusal={grant.error}
            pointer="/member"
          >
            <Select
              ref={field.ref}
              onBlur={field.onBlur}
              value={field.value}
              onValueChange={field.onChange}
              disabled={candidates.status !== "success"}
              placeholder={
                candidates.status === "pending" ? t`Reading the members…` : t`Choose a member`
              }
            >
              {offered.map((candidate) => {
                const name = memberName(candidate);
                const { email } = candidate;
                const label = candidate.display_name ? t`${name} (${email})` : email;
                const status = memberStatusName(i18n, candidate.status);
                return (
                  <SelectItem key={candidate.id} value={candidate.id}>
                    {candidate.status === "active" ? label : t`${label}: ${status}`}
                  </SelectItem>
                );
              })}
            </Select>
          </Field>
        )}
      />
      {candidates.status === "error" ? (
        <QueryError error={candidates.error} onRetry={candidates.retry} />
      ) : null}
      <Controller
        control={form.control}
        name="role"
        render={({ field }) => (
          <RoleField
            roles={roles}
            value={field.value}
            onChange={field.onChange}
            onBlur={field.onBlur}
            ref={field.ref}
            error={errors.role?.message}
            refusal={grant.error}
          />
        )}
      />
    </DialogForm>
  );
}
