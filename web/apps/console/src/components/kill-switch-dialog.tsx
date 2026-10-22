import { type Flag, killFlag } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogActions } from "@/components/ui/dialog";
import { FormRefusal } from "@/components/ui/form-page";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { formatDateTime } from "@/i18n/format";
import { unwrap } from "@/lib/errors";
import {
  killReasonMinimum,
  killReasonReady,
  lastKillOf,
  withConfiguration,
} from "@/lib/flag-detail";
import { auditQueryKey, flagEntriesQuery, flagQuery, flagsQueryKey } from "@/lib/queries";

export type KillSwitchDialogProps = {
  project: string;
  flag: Flag;
  environment: { id: string; key: string; name: string };
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

/**
 * The kill switch of a flag in one environment: it switches the flag off
 * there with a reason, whatever version the page holds. It is never shown
 * as done before the server says so, since an on-call engineer must know
 * the flag is off rather than hope it is.
 */
export function KillSwitchDialog({
  open,
  onOpenChange,
  flag,
  environment,
  ...rest
}: KillSwitchDialogProps) {
  const { t } = useLingui();
  const flagKey = flag.key;
  const name = environment.name;
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={t`Kill switch: ${flagKey} in ${name}`}>
      {open ? (
        <KillSwitch
          flag={flag}
          environment={environment}
          onDone={() => onOpenChange(false)}
          {...rest}
        />
      ) : null}
    </Dialog>
  );
}

function KillSwitch({
  project,
  flag,
  environment,
  onDone,
}: Omit<KillSwitchDialogProps, "open" | "onOpenChange"> & { onDone: () => void }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const field = useId();
  const [reason, setReason] = useState("");
  const lastUse = useQuery(
    flagEntriesQuery(project, flag.id, { environment: environment.id, killSwitch: true, limit: 1 }),
  );
  const last = lastKillOf(lastUse.data?.[0]);
  const missing = Math.max(0, killReasonMinimum - reason.trim().length);
  const name = environment.name;
  const who = last?.who;
  const when = last ? formatDateTime(i18n, last.at) : "";
  const lastReason = last?.reason;

  const kill = useMutation({
    mutationFn: () =>
      unwrap(
        killFlag({
          path: { project, flag: flag.key, environment: environment.key },
          body: { reason: reason.trim() },
        }),
      ),
    meta: { toast: false },
    onSuccess: (result) => {
      const key = flag.key;
      queryClient.setQueryData(
        flagQuery(project, flag.key).queryKey,
        (current: Flag | undefined) =>
          current ? withConfiguration(current, result.configuration) : current,
      );
      void queryClient.invalidateQueries({ queryKey: [...flagsQueryKey(project), "list"] });
      void queryClient.invalidateQueries({ queryKey: auditQueryKey });
      if (result.changed) toast.success(t`${key} is off in ${name}.`);
      else toast.info(t`${key} was already off in ${name}. Nothing changed.`);
      onDone();
    },
  });

  return (
    <>
      <div className="flex flex-col gap-2 text-sm text-muted-foreground">
        <p>
          <Trans>
            Everyone receives the off variation in {name}, whatever the rules and overrides say.
            Switching the flag back on is an ordinary configuration change.
          </Trans>
        </p>
        <p>
          {last ? (
            lastReason ? (
              <Trans>
                Last used by {who} on {when}: “{lastReason}”
              </Trans>
            ) : (
              <Trans>
                Last used by {who} on {when}, with no reason.
              </Trans>
            )
          ) : lastUse.isSuccess ? (
            <Trans>Never used on this flag in {name}.</Trans>
          ) : null}
        </p>
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor={field}>
          <Trans>Reason</Trans>
        </Label>
        <Textarea
          id={field}
          rows={3}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          aria-describedby={`${field}-hint`}
        />
        <p id={`${field}-hint`} className="text-sm text-muted-foreground">
          {missing > 0 ? (
            <Trans>Recorded in the audit log. Characters still needed: {missing}.</Trans>
          ) : (
            <Trans>Recorded in the audit log.</Trans>
          )}
        </p>
      </div>
      <FormRefusal refusal={kill.error} />
      <DialogActions>
        <Button variant="outline" onClick={onDone}>
          <Trans>Cancel</Trans>
        </Button>
        <Button
          variant="destructive"
          disabled={!killReasonReady(reason) || kill.isPending}
          onClick={() => kill.mutate()}
        >
          {kill.isPending ? <Trans>Switching off…</Trans> : <Trans>Switch off</Trans>}
        </Button>
      </DialogActions>
    </>
  );
}
