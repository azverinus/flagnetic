import { type Environment, setEnvironmentProtection } from "@flagnetic/api";
import { Card, CardContent } from "@/components/ui/card";
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
  // What the member was asked about, fixed when the dialog opened: another
  // owner's change reaching this screen meanwhile must not turn "Protect"
  // into "Stop protecting" under the member's hand. The version is the one
  // they saw, so a change made in between is refused, not overwritten.
  const [asking, setAsking] = useState<{ protect: boolean; version: number }>();
  const name = environment.name;
  const next = asking?.protect ?? !environment.protected;

  const change = useMutation({
    mutationFn: ({ protect, version }: { protect: boolean; version: number }) =>
      unwrap(
        setEnvironmentProtection({
          path: { project, environment: environment.key },
          body: { protected: protect },
          headers: ifMatch(version),
        }),
      ),
    meta: { toast: false },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: environmentsQuery(project).queryKey });
      setAsking(undefined);
      toast.success(next ? t`${name} is protected.` : t`${name} is no longer protected.`);
    },
  });

  return (
    <Card>
      <CardContent>
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
              setAsking({ protect: !environment.protected, version: environment.version });
            }}
          />
        </div>
        <ConfirmDialog
          open={asking !== undefined}
          onOpenChange={(open) => {
            if (!open) setAsking(undefined);
          }}
          title={next ? t`Protect ${name}` : t`Stop protecting ${name}`}
          confirmLabel={next ? <Trans>Protect environment</Trans> : <Trans>Stop protecting</Trans>}
          destructive={!next}
          pending={change.isPending}
          refusal={change.error}
          onConfirm={() => asking && change.mutate(asking)}
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
      </CardContent>
    </Card>
  );
}
