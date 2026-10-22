import { type Project, updateProject } from "@flagnetic/api";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { z } from "zod";

import { PermissionGate } from "@/components/can";
import { NamedConflictBanner } from "@/components/conflict-banner";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Textarea } from "@/components/ui/textarea";
import { UnsavedChangesBar } from "@/components/ui/unsaved-changes-bar";
import { unwrap } from "@/lib/errors";
import { changedFields, descriptionSchema, nameSchema } from "@/lib/forms";
import { ifMatch } from "@/lib/http";
import { projectQuery, projectsQuery } from "@/lib/queries";
import { useFormConflict } from "@/lib/use-form-conflict";

export const Route = createFileRoute("/_signed-in/projects/$project/settings")({
  staticData: { crumb: msg`Settings` },
  component: Settings,
});

function Settings() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  return (
    <PermissionGate permission="project.update" title={t`Settings`}>
      {/* Another project's settings start a form of their own: the values
          and the version they were read at belong to one project. */}
      <SettingsForm key={project} />
    </PermissionGate>
  );
}

/** The project's name and description; its key is fixed, since everything refers to it. */
function SettingsForm() {
  const { i18n, t } = useLingui();
  const { project: key } = Route.useParams();
  const queryClient = useQueryClient();
  const { data: project } = useSuspenseQuery(projectQuery(key));
  const schema = z.object({ name: nameSchema(i18n), description: descriptionSchema(i18n) });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: project.name, description: project.description },
  });
  // The project the form was filled from, which a save compares it with
  // to send only what the member changed, and the version the save names:
  // apart, since keeping one's edits over a newer version saves onto that
  // version what was changed from the older one, and apart from the query,
  // since a refetch that brings someone else's change must not make a save
  // overwrite it unseen.
  const [base, setBase] = useState(project);
  const [version, setVersion] = useState(project.version);
  const conflicts = useFormConflict({
    project: key,
    kind: "project",
    read: () => queryClient.fetchQuery({ ...projectQuery(key), staleTime: 0 }),
  });
  const restart = (from: Project) => {
    form.reset({ name: from.name, description: from.description });
    setBase(from);
    setVersion(from.version);
    conflicts.clear();
  };
  const save = useMutation({
    mutationFn: ({ values, at }: { values: Values; at: number }) =>
      unwrap(
        updateProject({
          path: { project: key },
          body: changedFields({ name: base.name, description: base.description }, values),
          headers: ifMatch(at),
        }),
      ),
    meta: { toast: false },
    onSuccess: (saved: Project) => {
      queryClient.setQueryData(projectQuery(key).queryKey, saved);
      void queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey, exact: true });
      restart(saved);
      toast.success(t`The project is saved.`);
    },
    onError: (error) => conflicts.meet(error),
  });
  const { errors, isDirty } = form.formState;
  const send = (at: number) =>
    form.handleSubmit((values) => {
      conflicts.clear();
      save.mutate({ values, at });
    });
  const submit = send(version);
  const conflict = conflicts.conflict;

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <PageHeader
        title={t`Settings`}
        description={<Trans>What the project is called and what it is for.</Trans>}
      />
      {conflict ? (
        <NamedConflictBanner
          base={base}
          conflict={conflict}
          pending={save.isPending}
          onReload={() => {
            queryClient.setQueryData(projectQuery(key).queryKey, conflict.current);
            save.reset();
            restart(conflict.current);
          }}
          onKeep={() => {
            setVersion(conflict.current.version);
            void send(conflict.current.version)();
          }}
        />
      ) : null}
      <FormPage
        onSubmit={submit}
        submitLabel={<Trans>Save</Trans>}
        pending={save.isPending}
        refusal={conflicts.refusal(save.error)}
      >
        <Field
          label={<Trans>Key</Trans>}
          hint={<Trans>Fixed once the project is created, since SDKs and scripts use it.</Trans>}
        >
          <Input value={project.key} disabled readOnly />
        </Field>
        <Field
          label={<Trans>Name</Trans>}
          error={errors.name?.message}
          refusal={save.error}
          pointer="/name"
        >
          <Input autoComplete="off" {...form.register("name")} />
        </Field>
        <Field
          label={<Trans>Description</Trans>}
          error={errors.description?.message}
          refusal={save.error}
          pointer="/description"
        >
          <Textarea rows={4} {...form.register("description")} />
        </Field>
      </FormPage>
      <UnsavedChangesBar dirty={isDirty} onDiscard={() => form.reset()} pending={save.isPending} />
    </div>
  );
}
