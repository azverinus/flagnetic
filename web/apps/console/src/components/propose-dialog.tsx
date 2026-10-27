import { type ChangeRequestCreate, createChangeRequest, type Violation } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, type ReactNode, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { toast } from "sonner";

import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, unwrap } from "@/lib/errors";
import { auditQueryKey, changeRequestQuery, changeRequestsQueryKey } from "@/lib/queries";

/** The longest title and description the server takes. */
const maxTitleLength = 200;
const maxDescriptionLength = 10_000;

/** What a change request proposes for which entity, as an editor has it. */
export type Proposal = Pick<
  ChangeRequestCreate,
  "kind" | "flag" | "environment" | "segment" | "experiment" | "proposed" | "revert_to"
>;

/** What the member says of a proposal. */
type Details = { title: string; description: string; applyAt: string };

export type ProposeDialogProps = {
  project: string;
  /** What is proposed; the dialog is open while there is one. */
  proposal: Proposal | undefined;
  onOpenChange: (open: boolean) => void;
  /**
   * A write the proposal needs first, such as a definition's name or
   * tags, which a change request does not carry. It is sent once, so
   * proposing again after a refusal does not repeat it.
   */
  before?: () => Promise<unknown>;
  /**
   * Called as the change request is created, before the console opens
   * it: the editor discards its draft, which the change request now holds.
   */
  onProposed: () => void;
  /**
   * Shows refusals of what is proposed where the editor shows a direct
   * write's, with their pointers as a direct write's: the dialog closes
   * so the member corrects the draft. Without it, the dialog says them.
   */
  onRefusedProposal?: (violations: Violation[]) => void;
  /** What the change is, above the form. */
  children?: ReactNode;
};

/**
 * Turns a change into a change request opened for review at once: the
 * member gives it a title, an optional description and an optional time
 * to apply it at once approved, and the console opens it.
 */
export function ProposeDialog({
  project,
  proposal,
  onOpenChange,
  before,
  onProposed,
  onRefusedProposal,
  children,
}: ProposeDialogProps) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  // Whether the write the proposal needs first is done: a refusal of the
  // proposal does not take it back.
  const prepared = useRef(false);

  const propose = useMutation({
    mutationFn: async ({ sent, details }: { sent: Proposal; details: Details }) => {
      if (before && !prepared.current) {
        await before();
        prepared.current = true;
      }
      const description = details.description.trim();
      return unwrap(
        createChangeRequest({
          path: { project },
          body: {
            ...sent,
            title: details.title.trim(),
            ...(description === "" ? {} : { description }),
            ...(details.applyAt === ""
              ? {}
              : { apply_at: new Date(details.applyAt).toISOString() }),
            open: true,
          },
        }),
      );
    },
    meta: { toast: false },
    onSuccess: async (created) => {
      queryClient.setQueryData(changeRequestQuery(project, created.id).queryKey, created);
      void queryClient.invalidateQueries({ queryKey: changeRequestsQueryKey(project) });
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      toast.success(t`The change request is open for review.`);
      // The draft goes into the change request, so leaving does not ask about it.
      flushSync(() => onProposed());
      prepared.current = false;
      await navigate({
        to: "/projects/$project/change-requests/$changeRequest",
        params: { project, changeRequest: created.id },
      });
    },
    onError: (error) => {
      if (!(error instanceof ApiError) || onRefusedProposal === undefined) return;
      const { violations } = error;
      if (violations.length === 0 || !violations.every((v) => v.pointer.startsWith("/proposed/"))) {
        return;
      }
      onRefusedProposal(
        violations.map((violation) => ({
          ...violation,
          pointer: violation.pointer.slice("/proposed".length),
        })),
      );
      propose.reset();
      onOpenChange(false);
    },
  });

  return (
    <Dialog
      open={proposal !== undefined}
      onOpenChange={(open) => {
        // Opened from outside, the dialog forgets a refusal as it closes,
        // so opened again it starts afresh.
        if (!open) propose.reset();
        onOpenChange(open);
      }}
      title={t`Propose the change`}
      description={
        <Trans>
          It is opened for review at once. Once a member who may approve change requests approves
          it, it is applied by hand or at the time given.
        </Trans>
      }
    >
      {proposal !== undefined ? (
        <ProposeForm
          pending={propose.isPending}
          refusal={propose.error}
          onCancel={() => onOpenChange(false)}
          onPropose={(details) => propose.mutate({ sent: proposal, details })}
        >
          {children}
        </ProposeForm>
      ) : null}
    </Dialog>
  );
}

function ProposeForm({
  pending,
  refusal,
  onCancel,
  onPropose,
  children,
}: {
  pending: boolean;
  refusal: unknown;
  onCancel: () => void;
  onPropose: (details: Details) => void;
  children: ReactNode;
}) {
  const { t } = useLingui();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [applyAt, setApplyAt] = useState("");
  const [checked, setChecked] = useState(false);
  const titleIssue = title.trim() === "" ? t`Enter a title.` : undefined;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    if (titleIssue === undefined) onPropose({ title, description, applyAt });
  };
  return (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel}
      submitLabel={<Trans>Propose</Trans>}
      pending={pending}
      refusal={refusal}
    >
      {children}
      <Field
        label={<Trans>Title</Trans>}
        error={checked ? titleIssue : undefined}
        refusal={refusal}
        pointer="/title"
      >
        <Input
          value={title}
          maxLength={maxTitleLength}
          onChange={(event) => setTitle(event.target.value)}
        />
      </Field>
      <Field
        label={<Trans>Description</Trans>}
        hint={<Trans>Why the change is needed, for whoever reviews it. Optional.</Trans>}
        refusal={refusal}
        pointer="/description"
      >
        <Textarea
          rows={4}
          maxLength={maxDescriptionLength}
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
      </Field>
      <Field
        label={<Trans>Apply at</Trans>}
        hint={
          <Trans>
            Optional, in your time zone: once approved, it is applied then. Left empty, it is
            applied by hand.
          </Trans>
        }
        refusal={refusal}
        pointer="/apply_at"
        className="sm:w-72"
      >
        <Input
          type="datetime-local"
          value={applyAt}
          onChange={(event) => setApplyAt(event.target.value)}
        />
      </Field>
    </DialogForm>
  );
}
