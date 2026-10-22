import { createSegment } from "@flagnetic/api";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import { PermissionGate } from "@/components/can";
import { QueryError } from "@/components/query-error";
import { SegmentFields } from "@/components/segment-fields";
import { Field } from "@/components/ui/field";
import { FormPage } from "@/components/ui/form-page";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import type { AttributeOption, AttributeTypes } from "@/lib/conditions";
import { ApiError, unwrap } from "@/lib/errors";
import { keySchema } from "@/lib/forms";
import { segmentsQueryKey } from "@/lib/queries";
import {
  placeSegmentRefusal,
  segmentCreateOf,
  type SegmentDraft,
  segmentFieldIssues,
  segmentIssues,
  segmentViolationMessage,
} from "@/lib/segments";
import { useConditionAttributes } from "@/lib/use-condition-attributes";

/**
 * The page that creates a segment lives under `segments/-/` rather than
 * at `segments/new`, because `new` is a key a segment may have and that
 * segment's page is `segments/new`. A segment key starts with a letter,
 * so no segment's address passes through `segments/-`.
 */
export const Route = createFileRoute("/_signed-in/projects/$project/segments/-/new")({
  staticData: { crumb: msg`New segment` },
  component: NewSegment,
});

function NewSegment() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const declarations = useConditionAttributes(project);
  return (
    <PermissionGate permission="segment.edit" title={t`New segment`}>
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <PageHeader
          title={t`New segment`}
          description={
            <Trans>
              A named set of attribute conditions that rules of every flag can target by name.
            </Trans>
          }
        />
        {declarations.state === "failed" ? (
          <QueryError error={declarations.error} onRetry={declarations.retry} />
        ) : declarations.state === "loading" ? (
          <div className="flex flex-col gap-3" aria-busy>
            <Skeleton className="h-9 w-full max-w-2xl" />
            <Skeleton className="h-24 w-full max-w-2xl" />
          </div>
        ) : (
          <CreateSegment
            key={project}
            project={project}
            attributes={declarations.options}
            types={declarations.types}
          />
        )}
      </div>
    </PermissionGate>
  );
}

function CreateSegment({
  project,
  attributes,
  types,
}: {
  project: string;
  attributes: AttributeOption[];
  types: AttributeTypes;
}) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [key, setKey] = useState("");
  const [draft, setDraft] = useState<SegmentDraft>({
    name: "",
    description: "",
    private: false,
    conditions: [],
  });
  // What the member still has to fix shows once they have tried to create.
  const [checked, setChecked] = useState(false);

  const create = useMutation({
    mutationFn: (sent: SegmentDraft) =>
      unwrap(createSegment({ path: { project }, body: segmentCreateOf(key, sent, types) })),
    meta: { toast: false },
    onSuccess: async (segment) => {
      await queryClient.invalidateQueries({ queryKey: segmentsQueryKey(project) });
      await navigate({
        to: "/projects/$project/segments/$segment",
        params: { project, segment: segment.key },
      });
    },
  });

  const keyIssue = keySchema(i18n).safeParse(key).error?.issues[0]?.message;
  const issues = new Map([
    ...(keyIssue ? [["key", keyIssue] as const] : []),
    ...segmentFieldIssues(i18n, draft),
    ...segmentIssues(i18n, undefined, draft, types),
  ]);
  const refusal = create.error instanceof ApiError ? create.error : undefined;
  const taken = refusal?.code === "already_exists";
  const placed = create.variables
    ? placeSegmentRefusal(refusal?.violations ?? [], create.variables)
    : undefined;
  const errors = new Map<string, string>([
    ...[...(placed?.at ?? [])].map(
      ([at, violation]) => [at, segmentViolationMessage(i18n, violation)] as const,
    ),
    ...(taken ? [["key", t`This project already has a segment with that key.`] as const] : []),
    ...(checked ? issues : []),
  ]);
  // A refusal is explained where it points, and above the actions only when nothing shows it.
  const shownInPlace = taken || (placed !== undefined && placed.at.size > 0);

  return (
    <FormPage
      onSubmit={(event) => {
        event.preventDefault();
        setChecked(true);
        if (issues.size > 0) return;
        create.mutate(draft);
      }}
      onCancel={() => void navigate({ to: "/projects/$project/segments", params: { project } })}
      submitLabel={<Trans>Create segment</Trans>}
      pending={create.isPending}
      refusal={shownInPlace ? undefined : create.error}
    >
      <Field
        label={<Trans>Key</Trans>}
        hint={<Trans>How rules name the segment. It never changes.</Trans>}
        error={errors.get("key")}
      >
        <Input
          autoComplete="off"
          autoFocus
          className="font-mono"
          value={key}
          onChange={(event) => setKey(event.target.value)}
        />
      </Field>
      <SegmentFields
        draft={draft}
        onChange={(next) => {
          // A refusal points at conditions by their places, which adding
          // or removing one moves; it is dropped rather than shown on a
          // condition it was not about.
          if (next.conditions.length !== draft.conditions.length) create.reset();
          setDraft(next);
        }}
        attributes={attributes}
        errors={errors}
      />
    </FormPage>
  );
}
