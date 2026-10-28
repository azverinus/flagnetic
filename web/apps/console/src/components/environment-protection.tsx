import { type Environment, setEnvironmentProtection } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useCan, useMissingPermissionReason } from "@/lib/access";
import { unwrap } from "@/lib/errors";
import { ifMatch } from "@/lib/http";
import { environmentsQuery } from "@/lib/queries";

/**
 * Whether an environment is protected: an owner turns it on or off after
 * a confirmation that names what changes. Everyone else sees the state
 * and what it takes to change it. The switch only moves once the server
 * has answered, so it never claims a protection the environment lacks.
 */
export function EnvironmentProtection({
  project,
  environment,
}: {
  project: string;
  environment: Environment;
}) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const label = useId();
  const can = useCan("environment.protect");
  const reason = useMissingPermissionReason("environment.protect");
  const [asking, setAsking] = useState(false);
  const name = environment.name;
  const next = !environment.protected;

  const change = useMutation({
    mutationFn: () =>
      unwrap(
        setEnvironmentProtection({
          path: { project, environment: environment.key },
          body: { protected: next },
          headers: ifMatch(environment.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: environmentsQuery(project).queryKey });
      setAsking(false);
      toast.success(next ? t`${name} is protected.` : t`${name} is no longer protected.`);
    },
  });

  return (
    <section className="flex flex-col gap-3 rounded-md border p-4">
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <Label htmlFor={label}>
            <Trans>Protected</Trans>
          </Label>
          <p id={`${label}-hint`} className="text-sm text-muted-foreground">
            <Trans>
              A change to what SDKs receive from a protected environment goes through a change
              request that someone else approves. The kill switch and import skip it.
            </Trans>
          </p>
          {can ? null : <p className="text-sm text-muted-foreground">{reason}</p>}
        </div>
        <Switch
          id={label}
          aria-describedby={`${label}-hint`}
          checked={environment.protected}
          disabled={!can}
          onCheckedChange={() => {
            change.reset();
            setAsking(true);
          }}
        />
      </div>
      <ConfirmDialog
        open={asking}
        onOpenChange={setAsking}
        title={next ? t`Protect ${name}` : t`Stop protecting ${name}`}
        confirmLabel={next ? <Trans>Protect environment</Trans> : <Trans>Stop protecting</Trans>}
        destructive={!next}
        pending={change.isPending}
        refusal={change.error}
        onConfirm={() => change.mutate()}
      >
        <p>
          {next ? (
            <Trans>
              Writes that would change what SDKs receive from {name} are refused until they come
              through an approved change request. SDKs ignore local overrides there.
            </Trans>
          ) : (
            <Trans>
              Anyone allowed to edit flags can change what SDKs receive from {name} directly, with
              no review. SDKs honour local overrides there again.
            </Trans>
          )}
        </p>
      </ConfirmDialog>
    </section>
  );
}
