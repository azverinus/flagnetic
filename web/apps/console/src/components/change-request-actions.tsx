import {
  applyChangeRequest,
  approveChangeRequest,
  cancelChangeRequest,
  type ChangeRequest,
  copyChangeRequest,
  openChangeRequest,
  rebaseChangeRequest,
  rejectChangeRequest,
  updateChangeRequest,
} from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, type ReactNode, useContext, useState } from "react";
import { toast } from "sonner";

import { DisabledAction } from "@/components/can";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog } from "@/components/ui/dialog";
import { DialogForm } from "@/components/ui/dialog-form";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { AccessContext, missingPermissionReason } from "@/lib/access";
import {
  type ActionRefusal,
  type ChangeRequestAction,
  changeRequestOffers,
} from "@/lib/change-requests";
import { ApiError, unwrap } from "@/lib/errors";
import { ifMatch } from "@/lib/http";
import {
  auditQueryKey,
  changeRequestQuery,
  changeRequestsQueryKey,
  flagsQueryKey,
  meQuery,
} from "@/lib/queries";

/** The longest title and description, and the longest comment of a decision, the server takes. */
const maxTitleLength = 200;
const maxDescriptionLength = 10_000;
const maxCommentLength = 2_000;

/** A write of one change request under the version the page shows. */
type ChangeRequestWrite =
  | { action: "edit"; title: string; description: string }
  | { action: "open" | "rebase" | "apply" | "cancel" }
  | { action: "approve" | "reject"; comment: string };

/**
 * Sends one write of a change request under the version the page shows,
 * and puts the change request it answers on the page. Applying changes the
 * flag's configuration, so the flag is read again. A refusal for a newer
 * version reads that version, so the page shows what someone else did and
 * confirming again is based on it.
 */
function useChangeRequestWrite(project: string, changeRequest: ChangeRequest) {
  const queryClient = useQueryClient();
  const { i18n } = useLingui();
  const detail = changeRequestQuery(project, changeRequest.id);

  return useMutation({
    mutationFn: (write: ChangeRequestWrite) => {
      const options = {
        path: { project, change_request: changeRequest.id },
        headers: ifMatch(changeRequest.version),
      };
      switch (write.action) {
        case "edit":
          return unwrap(
            updateChangeRequest({
              ...options,
              body: { title: write.title, description: write.description },
            }),
          );
        case "open":
          return unwrap(openChangeRequest(options));
        case "rebase":
          return unwrap(rebaseChangeRequest(options));
        case "apply":
          return unwrap(applyChangeRequest(options));
        case "cancel":
          return unwrap(cancelChangeRequest(options));
        case "approve":
          return unwrap(
            approveChangeRequest({
              ...options,
              body: write.comment === "" ? {} : { comment: write.comment },
            }),
          );
        case "reject":
          return unwrap(rejectChangeRequest({ ...options, body: { comment: write.comment } }));
      }
    },
    meta: { toast: false },
    onSuccess: (saved, write) => {
      queryClient.setQueryData(detail.queryKey, saved);
      void queryClient.invalidateQueries({ queryKey: changeRequestsQueryKey(project) });
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      if (write.action === "apply") {
        void queryClient.invalidateQueries({ queryKey: flagsQueryKey(project) });
      }
      toast.success(doneMessage(i18n, write.action));
    },
    onError: async (error) => {
      // A change request whose base moved is written superseded by the
      // refusal itself, and a newer version is someone else's write:
      // either way the page reads it again.
      if (
        !(error instanceof ApiError) ||
        (error.code !== "version_mismatch" && error.code !== "base_moved")
      ) {
        return;
      }
      await queryClient.invalidateQueries({ queryKey: detail.queryKey });
    },
  });
}

function doneMessage(i18n: I18n, action: ChangeRequestWrite["action"]): string {
  switch (action) {
    case "edit":
      return i18n._(msg`The change request is saved.`);
    case "open":
      return i18n._(msg`The change request is open for review.`);
    case "rebase":
      return i18n._(msg`The change request is based on the current configuration.`);
    case "approve":
      return i18n._(msg`The change request is approved.`);
    case "reject":
      return i18n._(msg`The change request is rejected.`);
    case "apply":
      return i18n._(msg`The change request is applied.`);
    case "cancel":
      return i18n._(msg`The change request is cancelled.`);
  }
}

/** Why an action is refused, as a sentence. */
function useRefusalReason(): (refusal: ActionRefusal) => string {
  const { i18n } = useLingui();
  const { roles } = useContext(AccessContext);
  return (refusal) =>
    refusal.kind === "own"
      ? i18n._(msg`You cannot approve or reject your own change request.`)
      : missingPermissionReason(i18n, refusal.permission, roles);
}

export type ChangeRequestActionsProps = {
  project: string;
  /** The change request as the page shows it; every write names its version. */
  changeRequest: ChangeRequest;
};

/**
 * The actions a change request offers the caller, each confirming what it
 * does: those only its author may take are shown to the author alone, and
 * those a permission allows are shown disabled, with why, to a caller
 * without it.
 */
export function ChangeRequestActions({ project, changeRequest }: ChangeRequestActionsProps) {
  const { t } = useLingui();
  const access = useContext(AccessContext);
  const me = useQuery(meQuery);
  const reason = useRefusalReason();
  const [asking, setAsking] = useState<ChangeRequestAction>();
  const write = useChangeRequestWrite(project, changeRequest);
  const offers = changeRequestOffers(changeRequest, {
    member: me.data?.member?.id,
    permissions: access.permissions,
  });

  const ask = (action: ChangeRequestAction) => {
    write.reset();
    setAsking(action);
  };
  const dialog = (action: ChangeRequestAction) => ({
    open: asking === action,
    onOpenChange: (open: boolean) => !open && setAsking(undefined),
  });
  const close = () => setAsking(undefined);
  const send = (next: ChangeRequestWrite) => write.mutate(next, { onSuccess: close });

  const labels: Record<ChangeRequestAction, ReactNode> = {
    edit: <Trans>Edit</Trans>,
    open: <Trans>Open for review</Trans>,
    rebase: <Trans>Rebase</Trans>,
    approve: <Trans>Approve</Trans>,
    reject: <Trans>Reject</Trans>,
    apply: <Trans>Apply</Trans>,
    cancel: <Trans>Cancel change request</Trans>,
    copy: <Trans>Copy</Trans>,
  };
  const primary = new Set<ChangeRequestAction>(["open", "approve", "apply"]);

  return (
    <>
      {offers.map(({ action, refusal }) => {
        const button = (
          <Button
            key={action}
            variant={primary.has(action) ? "default" : "outline"}
            onClick={() => ask(action)}
          >
            {labels[action]}
          </Button>
        );
        return refusal === undefined ? (
          button
        ) : (
          <DisabledAction key={action} reason={reason(refusal)}>
            {button}
          </DisabledAction>
        );
      })}

      <Dialog {...dialog("edit")} title={t`Edit the change request`}>
        {asking === "edit" ? (
          <EditForm
            changeRequest={changeRequest}
            pending={write.isPending}
            refusal={write.error}
            onCancel={close}
            onSave={(title, description) => send({ action: "edit", title, description })}
          />
        ) : null}
      </Dialog>
      <ConfirmDialog
        {...dialog("open")}
        title={t`Open the change request for review`}
        confirmLabel={<Trans>Open for review</Trans>}
        pending={write.isPending}
        refusal={write.error}
        onConfirm={() => send({ action: "open" })}
      >
        <p>
          <Trans>
            Members who may approve change requests can then approve or reject it. Its proposal can
            still be edited while it is open.
          </Trans>
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        {...dialog("rebase")}
        title={t`Rebase the change request`}
        confirmLabel={<Trans>Rebase</Trans>}
        pending={write.isPending}
        refusal={write.error}
        onConfirm={() => send({ action: "rebase" })}
      >
        <p>
          <Trans>
            The change request is based on the configuration as it is now; its diff is computed
            again from it.
          </Trans>
        </p>
      </ConfirmDialog>
      <Dialog {...dialog("approve")} title={t`Approve the change request`}>
        {asking === "approve" ? (
          <DecisionForm
            decision="approve"
            pending={write.isPending}
            refusal={write.error}
            onCancel={close}
            onDecide={(comment) => send({ action: "approve", comment })}
          />
        ) : null}
      </Dialog>
      <Dialog {...dialog("reject")} title={t`Reject the change request`}>
        {asking === "reject" ? (
          <DecisionForm
            decision="reject"
            pending={write.isPending}
            refusal={write.error}
            onCancel={close}
            onDecide={(comment) => send({ action: "reject", comment })}
          />
        ) : null}
      </Dialog>
      <ConfirmDialog
        {...dialog("apply")}
        title={t`Apply the change request`}
        confirmLabel={<Trans>Apply</Trans>}
        pending={write.isPending}
        refusal={write.error}
        onConfirm={() => send({ action: "apply" })}
      >
        <p>
          <Trans>
            The proposed configuration is written to the environment now, and its SDKs receive it.
          </Trans>
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        {...dialog("cancel")}
        title={t`Cancel the change request`}
        confirmLabel={<Trans>Cancel change request</Trans>}
        destructive
        pending={write.isPending}
        refusal={write.error}
        onConfirm={() => send({ action: "cancel" })}
      >
        <p>
          <Trans>
            The change request leaves the queue and is never applied. It can still be copied into a
            new draft.
          </Trans>
        </p>
      </ConfirmDialog>
      <CopyDialog {...dialog("copy")} project={project} changeRequest={changeRequest} />
    </>
  );
}

function EditForm({
  changeRequest,
  pending,
  refusal,
  onCancel,
  onSave,
}: {
  changeRequest: ChangeRequest;
  pending: boolean;
  refusal: unknown;
  onCancel: () => void;
  onSave: (title: string, description: string) => void;
}) {
  const { t } = useLingui();
  const [title, setTitle] = useState(changeRequest.title);
  const [description, setDescription] = useState(changeRequest.description);
  const [checked, setChecked] = useState(false);
  const titleIssue = title.trim() === "" ? t`Enter a title.` : undefined;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    if (titleIssue === undefined) onSave(title.trim(), description.trim());
  };
  return (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel}
      submitLabel={<Trans>Save</Trans>}
      pending={pending}
      refusal={refusal}
    >
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
          rows={5}
          maxLength={maxDescriptionLength}
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
      </Field>
    </DialogForm>
  );
}

/** An approval, whose comment is optional, or a rejection, whose comment says why. */
export function DecisionForm({
  decision,
  pending,
  refusal,
  onCancel,
  onDecide,
}: {
  decision: "approve" | "reject";
  pending: boolean;
  refusal: unknown;
  onCancel: () => void;
  onDecide: (comment: string) => void;
}) {
  const { t } = useLingui();
  const [comment, setComment] = useState("");
  const [checked, setChecked] = useState(false);
  const issue =
    decision === "reject" && comment.trim() === ""
      ? t`Say why, so the author knows what to change.`
      : undefined;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setChecked(true);
    if (issue === undefined) onDecide(comment.trim());
  };
  return (
    <DialogForm
      onSubmit={submit}
      onCancel={onCancel}
      submitLabel={decision === "approve" ? <Trans>Approve</Trans> : <Trans>Reject</Trans>}
      pending={pending}
      refusal={refusal}
    >
      <Field
        label={<Trans>Comment</Trans>}
        hint={
          decision === "approve" ? (
            <Trans>Optional; the author and the audit log see it.</Trans>
          ) : (
            <Trans>Required; the author and the audit log see it.</Trans>
          )
        }
        error={checked ? issue : undefined}
        refusal={refusal}
        pointer="/comment"
      >
        <Textarea
          rows={4}
          maxLength={maxCommentLength}
          value={comment}
          onChange={(event) => setComment(event.target.value)}
        />
      </Field>
    </DialogForm>
  );
}

function CopyDialog({
  open,
  onOpenChange,
  project,
  changeRequest,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  project: string;
  changeRequest: ChangeRequest;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const copy = useMutation({
    mutationFn: () =>
      unwrap(copyChangeRequest({ path: { project, change_request: changeRequest.id } })),
    meta: { toast: false },
    onSuccess: async (created) => {
      queryClient.setQueryData(changeRequestQuery(project, created.id).queryKey, created);
      void queryClient.invalidateQueries({ queryKey: changeRequestsQueryKey(project) });
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      toast.success(t`The copy is a new draft.`);
      onOpenChange(false);
      await navigate({
        to: "/projects/$project/change-requests/$changeRequest",
        params: { project, changeRequest: created.id },
      });
    },
  });
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(next) => {
        if (next) copy.reset();
        onOpenChange(next);
      }}
      title={t`Copy the change request`}
      confirmLabel={<Trans>Copy</Trans>}
      pending={copy.isPending}
      refusal={copy.error}
      onConfirm={() => copy.mutate()}
    >
      <p>
        <Trans>
          A new draft of yours proposes the same configuration, based on the configuration as it is
          now. This change request stays as it is.
        </Trans>
      </p>
    </ConfirmDialog>
  );
}
