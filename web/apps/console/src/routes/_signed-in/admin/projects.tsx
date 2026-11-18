import { createProject } from "@flagnetic/api";
import { Page } from "@/components/ui/page";
import { zodResolver } from "@hookform/resolvers/zod";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useId } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { CreateFromDocument } from "@/components/create-from-document";
import { ProjectsTable } from "@/components/projects-table";
import { QueryError } from "@/components/query-error";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, unwrap } from "@/lib/errors";
import { descriptionSchema, keySchema, nameSchema } from "@/lib/forms";
import { projectsQuery } from "@/lib/queries";

export const Route = createFileRoute("/_signed-in/admin/projects")({
  staticData: { crumb: msg`Projects` },
  component: InstanceProjects,
});

/**
 * Every project of the instance, and the form that creates one with its
 * default environments, unit types and standard attributes.
 */
function InstanceProjects() {
  const { t } = useLingui();
  const projects = useQuery(projectsQuery);
  const newProject = useId();
  const fromDocument = useId();
  const allProjects = useId();

  return (
    <Page>
      <PageHeader
        title={t`Projects`}
        description={
          <Trans>
            A new project starts with the environments development, staging and production, the unit
            types user and anonymous, and the standard attributes.
          </Trans>
        }
      />
      <Card aria-labelledby={newProject}>
        <CardHeader titleId={newProject} title={<Trans>New project</Trans>} />
        <CardContent>
          <CreateProject />
        </CardContent>
      </Card>
      <Card aria-labelledby={fromDocument}>
        <CardHeader titleId={fromDocument} title={<Trans>Create from a document</Trans>} />
        <CardContent>
          <p className="text-sm text-muted-foreground">
            <Trans>
              Starts a project from a project document, such as the export of a project of this or
              another instance. The project holds exactly what the document names, plus the standard
              attributes.
            </Trans>
          </p>
          <CreateFromDocument />
        </CardContent>
      </Card>
      <Card aria-labelledby={allProjects}>
        <CardHeader titleId={allProjects} title={<Trans>All projects</Trans>} />
        <CardContent>
          {projects.isError ? (
            <QueryError error={projects.error} onRetry={() => void projects.refetch()} />
          ) : (
            <ProjectsTable
              caption={t`All projects`}
              projects={projects.data ?? []}
              loading={projects.isPending}
              empty={<Trans>No projects yet</Trans>}
            />
          )}
        </CardContent>
      </Card>
    </Page>
  );
}

function CreateProject() {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const schema = z.object({
    key: keySchema(i18n),
    name: nameSchema(i18n),
    description: descriptionSchema(i18n),
  });
  type Values = z.infer<typeof schema>;
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { key: "", name: "", description: "" },
  });
  const create = useMutation({
    mutationFn: (body: Values) => unwrap(createProject({ body })),
    meta: { toast: false },
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey });
      await navigate({ to: "/projects/$project", params: { project: project.key } });
    },
  });
  const taken = create.error instanceof ApiError && create.error.code === "already_exists";
  const { errors } = form.formState;

  return (
    <FormPage
      onSubmit={form.handleSubmit((values) => create.mutate(values))}
      submitLabel={<Trans>Create project</Trans>}
      pending={create.isPending}
      refusal={taken ? undefined : create.error}
    >
      <Field
        label={<Trans>Key</Trans>}
        hint={
          <Trans>
            How SDKs and scripts name the project: lowercase letters, digits, - and _. It never
            changes.
          </Trans>
        }
        error={
          errors.key?.message ??
          (taken ? t`This instance already has a project with that key.` : undefined)
        }
        refusal={create.error}
        pointer="/key"
      >
        <Input autoComplete="off" {...form.register("key")} />
      </Field>
      <Field
        label={<Trans>Name</Trans>}
        error={errors.name?.message}
        refusal={create.error}
        pointer="/name"
      >
        <Input autoComplete="off" {...form.register("name")} />
      </Field>
      <Field
        label={<Trans>Description</Trans>}
        error={errors.description?.message}
        refusal={create.error}
        pointer="/description"
      >
        <Textarea rows={2} {...form.register("description")} />
      </Field>
    </FormPage>
  );
}
