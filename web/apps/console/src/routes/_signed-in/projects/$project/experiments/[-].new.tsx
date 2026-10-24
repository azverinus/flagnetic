import { createExperiment, type ExperimentCreate } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import { PermissionGate } from "@/components/can";
import { ExperimentFields } from "@/components/experiment-fields";
import { QueryError } from "@/components/query-error";
import { FormPage } from "@/components/ui/form-page";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError, unwrap } from "@/lib/errors";
import {
  experimentFieldPointer,
  experimentIssues,
  type ExperimentValues,
  parsePercent,
  weightsOf,
} from "@/lib/experiments";
import { memberName } from "@/lib/members";
import { experimentsQueryKey, meQuery, projectMembersQuery } from "@/lib/queries";
import { useExperimentChoices } from "@/lib/use-experiment-choices";

/**
 * The page that creates an experiment lives under `experiments/-/` rather
 * than at `experiments/new`, because `new` is a key an experiment may have
 * and that experiment's page is `experiments/new`. An experiment key starts
 * with a letter, so no experiment's address passes through `experiments/-`.
 */
export const Route = createFileRoute("/_signed-in/projects/$project/experiments/-/new")({
  staticData: { crumb: msg`New experiment` },
  component: NewExperiment,
});

function NewExperiment() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  return (
    <PermissionGate permission="experiment.edit" title={t`New experiment`}>
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <PageHeader
          title={t`New experiment`}
          description={
            <Trans>
              The experiment is created as a draft: nothing changes for users until a rule serves it
              and it starts.
            </Trans>
          }
        />
        <CreateExperiment key={project} project={project} />
      </div>
    </PermissionGate>
  );
}

/** The owner's value for the member creating the experiment, whom the server names when none is sent. */
const me = "me";

const emptyValues: ExperimentValues = {
  key: "",
  name: "",
  hypothesis: "",
  owner: me,
  tags: [],
  flag: "",
  unit_type: "",
  variations: [],
  traffic_allocation: "100",
  expected_end_at: "",
};

function CreateExperiment({ project }: { project: string }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { data: caller } = useSuspenseQuery(meQuery);
  const members = useQuery(projectMembersQuery(project));
  const choices = useExperimentChoices(project);
  const [values, setValues] = useState(emptyValues);
  const [checked, setChecked] = useState(false);

  const create = useMutation({
    mutationFn: (sent: ExperimentValues) => {
      const body: ExperimentCreate = {
        key: sent.key.trim(),
        name: sent.name.trim(),
        hypothesis: sent.hypothesis.trim(),
        flag: sent.flag,
        unit_type: sent.unit_type,
        variations: weightsOf(sent.variations),
        traffic_allocation: parsePercent(sent.traffic_allocation),
        expected_end_at: sent.expected_end_at,
        ...(sent.owner !== me ? { owner: sent.owner } : {}),
        ...(sent.tags.length > 0 ? { tags: sent.tags } : {}),
      };
      return unwrap(createExperiment({ path: { project }, body }));
    },
    meta: { toast: false },
    onSuccess: async (experiment) => {
      await queryClient.invalidateQueries({ queryKey: experimentsQueryKey(project) });
      const key = experiment.key;
      toast.success(t`The experiment ${key} is created.`);
      await navigate({
        to: "/projects/$project/experiments/$experiment",
        params: { project, experiment: key },
      });
    },
  });

  const owners = useMemo(() => {
    const myName = caller.member ? memberName(caller.member) : "";
    const others = (members.data ?? [])
      .filter((member) => member.status === "active" && member.member !== caller.member?.id)
      .map((member) => ({ value: member.member, label: memberName(member) }))
      .sort((a, b) => a.label.localeCompare(b.label, i18n.locale));
    return [{ value: me, label: i18n._(msg`Me (${myName})`) }, ...others];
  }, [members.data, caller.member, i18n]);

  if (choices.state === "failed") {
    return <QueryError error={choices.error} onRetry={choices.retry} />;
  }
  if (choices.state === "loading") {
    return (
      <div className="flex flex-col gap-3" aria-busy>
        <Skeleton className="h-9 w-full max-w-2xl" />
        <Skeleton className="h-24 w-full max-w-2xl" />
      </div>
    );
  }

  const variations =
    choices.flags.find((flag) => flag.key === values.flag)?.variations.map(({ key }) => key) ?? [];
  const issues = experimentIssues(i18n, values, { variations });
  const refusal = create.error;
  const taken = refusal instanceof ApiError && refusal.code === "already_exists";
  // A refusal is explained under the fields it names, and above the
  // actions only when no field shows it.
  const shownAtFields =
    taken ||
    (refusal instanceof ApiError &&
      refusal.violations.some((violation) => experimentFieldPointer.test(violation.pointer)));
  if (taken) issues.fields.set("key", t`This project already has an experiment with that key.`);

  return (
    <FormPage
      className="max-w-3xl"
      onSubmit={(event) => {
        event.preventDefault();
        setChecked(true);
        // A key the server found taken would only be refused again.
        if (issues.any || taken) return;
        create.mutate(values);
      }}
      onCancel={() => void navigate({ to: "/projects/$project/experiments", params: { project } })}
      submitLabel={<Trans>Create experiment</Trans>}
      pending={create.isPending}
      refusal={shownAtFields ? undefined : refusal}
    >
      <ExperimentFields
        values={values}
        onChange={(next) => {
          // A refusal points at weights by their places, which a new flag
          // or a row added or removed moves; it is dropped rather than
          // shown on a row it was not about.
          if (next.variations.length !== values.variations.length || next.flag !== values.flag) {
            create.reset();
          }
          // A key the server found taken is taken no longer once changed.
          if (taken && next.key !== values.key) create.reset();
          setValues(next);
        }}
        issues={checked ? issues : undefined}
        refusal={refusal}
        flags={choices.flags}
        unitTypes={choices.unitTypes}
        owners={owners}
      />
    </FormPage>
  );
}
