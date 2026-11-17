import {
  type Environment,
  type Flag,
  type FlagConfiguration,
  putFlagConfiguration,
} from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { OctagonXIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { KillSwitchDialog } from "@/components/kill-switch-dialog";
import { type Proposal, ProposeDialog } from "@/components/propose-dialog";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ApiError, unwrap } from "@/lib/errors";
import { configurationPut } from "@/lib/flag-detail";
import { ifMatch } from "@/lib/http";
import { problemMessage } from "@/lib/problem-messages";
import { flagQuery } from "@/lib/queries";
import { useConfigurationAnswer } from "@/lib/use-configuration-answer";

export type FlagOnOffProps = {
  project: string;
  flag: Flag;
  configuration: FlagConfiguration;
  /** The environment, once the project's environments are read. */
  environment: Environment | undefined;
  /** Reading the environments again, and why they could not be read. */
  environmentsRead: { error: unknown; retry: () => void };
  /** Why the configuration cannot be changed now, such as the flag being archived. */
  frozen: boolean;
};

/**
 * Switching the flag on or off in the environment, which is saved at
 * once, and the kill switch beside it while it is on.
 */
export function FlagOnOff({
  project,
  flag,
  configuration,
  environment,
  environmentsRead,
  frozen,
}: FlagOnOffProps) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const answered = useConfigurationAnswer(project, flag.key);
  const [killing, setKilling] = useState(false);
  const [proposing, setProposing] = useState<Proposal>();
  const name = environment?.name ?? configuration.environment;
  const key = flag.key;

  const toggle = useMutation({
    mutationFn: (on: boolean) =>
      unwrap(
        putFlagConfiguration({
          path: { project, flag: flag.key, environment: configuration.environment },
          body: { ...configurationPut(configuration), on },
          headers: ifMatch(configuration.version),
        }),
      ),
    meta: { toast: false },
    onSuccess: (next) => {
      answered(next);
      toast.success(next.on ? t`${key} is on in ${name}.` : t`${key} is off in ${name}.`);
    },
    onError: (error, on) => {
      if (error instanceof ApiError && error.code === "version_mismatch") {
        void queryClient.invalidateQueries({ queryKey: flagQuery(project, flag.key).queryKey });
        toast.error(
          t`Someone changed this configuration a moment ago. It is read again: switch it once more if you still mean to.`,
        );
        return;
      }
      // A protected environment is switched through review: the same
      // switch is offered as a change request.
      if (error instanceof ApiError && error.code === "change_request_required") {
        setProposing({
          kind: "flag_configuration",
          flag: flag.key,
          environment: configuration.environment,
          proposed: { ...configurationPut(configuration), on },
        });
        return;
      }
      toast.error(error instanceof ApiError ? problemMessage(i18n, error) : String(error));
    },
  });

  return (
    <section className="flex flex-wrap items-center justify-between gap-4 rounded-md border p-4">
      <div className="flex items-center gap-3">
        <Can permission="flag_configuration.update" whenMissing="disable">
          <Switch
            checked={configuration.on}
            disabled={frozen || toggle.isPending}
            // One name whatever the state, which the switch announces itself.
            aria-label={t`${key} on in ${name}`}
            onCheckedChange={(on) => toggle.mutate(on)}
          />
        </Can>
        <span className="text-sm font-medium">
          {configuration.on ? <Trans>On</Trans> : <Trans>Off</Trans>}
        </span>
        <span className="text-sm text-muted-foreground">
          {configuration.on ? (
            <Trans>Contexts are served by the overrides, rules and fallthrough below.</Trans>
          ) : (
            <Trans>Every context receives the off variation.</Trans>
          )}
        </span>
      </div>
      {configuration.on && !frozen ? (
        <>
          {/* The dialog needs the environment, which is read apart: until it is, the button is disabled and says it is waiting, rather than doing nothing. */}
          <Can permission="flag_configuration.kill" whenMissing="disable">
            <Button
              variant="destructive"
              disabled={!environment}
              aria-busy={!environment && !environmentsRead.error}
              onClick={() => setKilling(true)}
            >
              <OctagonXIcon aria-hidden />
              <Trans>Kill switch</Trans>
            </Button>
          </Can>
          {environment ? (
            <KillSwitchDialog
              project={project}
              flag={flag}
              environment={environment}
              open={killing}
              onOpenChange={setKilling}
            />
          ) : null}
        </>
      ) : null}
      <ProposeDialog
        project={project}
        proposal={proposing}
        onOpenChange={(open) => !open && setProposing(undefined)}
        onProposed={() => setProposing(undefined)}
      >
        <p className="text-sm">
          {configuration.on ? (
            <Trans>
              {name} is protected: switching {key} off there goes through a change request.
            </Trans>
          ) : (
            <Trans>
              {name} is protected: switching {key} on there goes through a change request.
            </Trans>
          )}
        </p>
      </ProposeDialog>
    </section>
  );
}
