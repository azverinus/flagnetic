import type { Project, Role } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { Trans, useLingui } from "@lingui/react/macro";
import type { ReactNode } from "react";
import { Controller, useForm, useWatch } from "react-hook-form";
import { z } from "zod";

import { memberFields } from "@/components/member-form";
import { QueryError } from "@/components/query-error";
import { RoleField } from "@/components/role-field";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Select, SelectItem } from "@/components/ui/select";
import { ApiError } from "@/lib/errors";
import { formRefusal } from "@/lib/problem-messages";

/** Who to invite and, when the invitation grants one, the role in which project. */
export type InviteValues = {
  email: string;
  display_name: string;
  /** The project's key; empty when the invitation grants no role. */
  project: string;
  /** The role's name; empty when the invitation grants no role. */
  role: string;
};

/** The projects an instance admin may invite to, as far as they are read. */
export type InviteProjects =
  | { status: "pending" }
  | { status: "error"; error: unknown; retry: () => void }
  | { status: "success"; projects: readonly Pick<Project, "key" | "name">[] };

export type InviteFormProps = {
  /**
   * The roles the invitation may grant: in a project, those the inviter
   * may grant there; to the instance, every role.
   */
  roles: readonly Role[];
  /**
   * The projects to choose from when inviting to the instance, where a
   * project and its role are optional; absent when inviting to one
   * project, where the role is required.
   */
  projects?: InviteProjects;
  pending: boolean;
  /** The refusal of the last submission. */
  refusal: unknown;
  onSubmit: (values: InviteValues) => void;
  onCancel: () => void;
};

/** The project choice meaning no project, as a choice cannot be empty. */
const noProject = "none";

/**
 * Inviting someone by email and name, in a dialog: to a project with one
 * role, or to the instance with a role in a project if the inviter
 * chooses one. An email the instance already holds is said under the
 * email, where it is corrected.
 */
export function InviteForm({
  roles,
  projects,
  pending,
  refusal,
  onSubmit,
  onCancel,
}: InviteFormProps) {
  const { i18n, t } = useLingui();
  const toInstance = projects !== undefined;
  const rolePointer = toInstance ? "/grant/role" : "/role";
  const fieldPointers = ["/email", "/display_name", rolePointer, "/grant/project"];
  const schema = z
    .object({ ...memberFields(i18n), project: z.string(), role: z.string() })
    .superRefine((values, context) => {
      // A role is chosen whenever the invitation is to a project: always
      // from a project's page, and once a project is chosen otherwise.
      if ((!toInstance || values.project !== "") && values.role === "") {
        context.addIssue({ code: "custom", path: ["role"], message: t`Choose a role.` });
      }
    });
  const form = useForm<InviteValues>({
    resolver: zodResolver(schema),
    defaultValues: { email: "", display_name: "", project: "", role: "" },
  });
  const project = useWatch({ control: form.control, name: "project" });
  const taken = refusal instanceof ApiError && refusal.code === "already_exists";
  const { errors } = form.formState;
  const offered = projects?.status === "success" ? projects.projects : [];

  const roleHint: ReactNode =
    toInstance && project === "" ? <Trans>Choose a project first.</Trans> : undefined;

  return (
    <DialogForm
      onSubmit={form.handleSubmit((values) =>
        onSubmit({
          email: values.email.trim(),
          display_name: values.display_name.trim(),
          project: values.project,
          role: values.project === "" && toInstance ? "" : values.role,
        }),
      )}
      onCancel={onCancel}
      submitLabel={<Trans>Invite</Trans>}
      pending={pending}
      refusal={taken ? undefined : formRefusal(refusal, fieldPointers)}
    >
      <Field
        label={<Trans>Email</Trans>}
        error={
          errors.email?.message ??
          (taken
            ? toInstance
              ? t`Another member already has this email address.`
              : t`Another member already has this email address. Grant them a role instead.`
            : undefined)
        }
        refusal={refusal}
        pointer="/email"
      >
        <Input type="email" autoComplete="off" {...form.register("email")} />
      </Field>
      <Field
        label={<Trans>Display name</Trans>}
        error={errors.display_name?.message}
        refusal={refusal}
        pointer="/display_name"
      >
        <Input autoComplete="off" {...form.register("display_name")} />
      </Field>
      {projects !== undefined ? (
        <>
          <Controller
            control={form.control}
            name="project"
            render={({ field }) => (
              <Field
                label={<Trans>Project</Trans>}
                hint={
                  <Trans>
                    Optional. Accepting the invitation grants the role you choose in this project.
                  </Trans>
                }
                refusal={refusal}
                pointer="/grant/project"
              >
                <Select
                  ref={field.ref}
                  onBlur={field.onBlur}
                  value={field.value === "" ? noProject : field.value}
                  onValueChange={(value) => {
                    field.onChange(value === noProject ? "" : value);
                    if (value === noProject) form.setValue("role", "");
                  }}
                  disabled={projects.status !== "success"}
                >
                  <SelectItem value={noProject}>{t`No project`}</SelectItem>
                  {offered.map((candidate) => (
                    <SelectItem key={candidate.key} value={candidate.key}>
                      {candidate.name}
                    </SelectItem>
                  ))}
                </Select>
              </Field>
            )}
          />
          {projects.status === "error" ? (
            <QueryError error={projects.error} onRetry={projects.retry} />
          ) : null}
        </>
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
            refusal={refusal}
            pointer={rolePointer}
            hint={roleHint}
            disabled={toInstance && project === ""}
          />
        )}
      />
    </DialogForm>
  );
}
