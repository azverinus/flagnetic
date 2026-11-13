import type { Sink } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { KeyRoundIcon, PlayIcon, PowerOffIcon, SendIcon, Trash2Icon } from "lucide-react";
import { type ReactNode, useState } from "react";
import { toast } from "sonner";

import { QueryError } from "@/components/query-error";
import { EnableSinkDialog, SampleSinkDialog } from "@/components/sink-actions";
import { SinkEditor } from "@/components/sink-editor";
import { DeadLettersTab, GapsTab, ReplayTab, SinkAlerts } from "@/components/sink-recovery";
import { SinkSecretDialog } from "@/components/sink-secret-dialog";
import { SinkStreams } from "@/components/sink-streams";
import { SinkAtRisk, SinkHealthBadge } from "@/components/sink-status";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { formatLag } from "@/i18n/format";
import { ApiError } from "@/lib/errors";
import { useListMutation } from "@/lib/mutations";
import { kindName, sinkApi, sinkKey, sinkQuery, sinksKey } from "@/lib/sinks";

/** The page that creates a sink; a webhook sink's secret is shown once before its page opens. */
export function NewSinkScreen({ project }: { project: string }) {
  const { t } = useLingui();
  const navigate = useNavigate();
  const [created, setCreated] = useState<{ id: string; secret: string }>();
  const open = (sink: string) =>
    void navigate({ to: "/projects/$project/sinks/$sink", params: { project, sink } });
  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-6 p-6">
      <PageHeader
        title={t`New sink`}
        description={
          <Trans>
            A new sink is disabled and delivers nothing until it is enabled. Saving is never held
            back by its target.
          </Trans>
        }
      />
      <SinkEditor
        project={project}
        onSaved={(saved) => {
          if ("secret" in saved && saved.secret) {
            setCreated({ id: saved.id, secret: saved.secret });
          } else {
            open(saved.id);
          }
        }}
      />
      <SinkSecretDialog
        secret={created?.secret}
        rotated={false}
        onClose={() => {
          if (created) open(created.id);
          setCreated(undefined);
        }}
      />
    </div>
  );
}

/** One sink: its settings, with rotating a webhook sink's secret and deleting it. */
export function SinkScreen({ project, sink: id }: { project: string; sink: string }) {
  const sink = useQuery(sinkQuery(project, id));
  if (sink.isError) {
    // An identifier that is no UUID is refused as a bad request: it names no sink either.
    const missing =
      sink.error instanceof ApiError && (sink.error.status === 404 || sink.error.status === 400);
    return missing ? (
      <SinkNotFound project={project} />
    ) : (
      <div className="mx-auto flex max-w-4xl flex-col gap-6 p-6">
        <QueryError error={sink.error} onRetry={() => void sink.refetch()} />
      </div>
    );
  }
  if (sink.data === undefined) {
    return (
      <div className="mx-auto flex max-w-4xl flex-col gap-6 p-6" aria-busy>
        <Skeleton className="h-9 w-64" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  return <SinkPage project={project} sink={sink.data} />;
}

function SinkNotFound({ project }: { project: string }) {
  const navigate = useNavigate();
  return (
    <div className="mx-auto flex max-w-xl flex-col items-start gap-3 p-6">
      <h1 className="text-xl font-semibold">
        <Trans>Sink not found</Trans>
      </h1>
      <p className="text-sm text-muted-foreground">
        <Trans>It does not exist here, or it was deleted.</Trans>
      </p>
      <Button
        variant="outline"
        onClick={() => void navigate({ to: "/projects/$project/sinks", params: { project } })}
      >
        <Trans>Go to the sinks</Trans>
      </Button>
    </div>
  );
}

function SinkPage({ project, sink }: { project: string; sink: Sink }) {
  const { i18n, t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const api = sinkApi(project);
  const [rotating, setRotating] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [enabling, setEnabling] = useState(false);
  const [disabling, setDisabling] = useState(false);
  const [sampling, setSampling] = useState(false);
  const [secret, setSecret] = useState<string>();

  /** Puts the sink a write answered in place of the one read. */
  const remember = (saved: Sink) => {
    queryClient.setQueryData(sinkQuery(project, sink.id).queryKey, saved);
    void queryClient.invalidateQueries({ queryKey: sinksKey(project), exact: true });
  };
  const rotate = useListMutation(
    () => api.rotateSecret(sink.id),
    sinkKey(project, sink.id),
    () => undefined,
    { secret: true },
  );
  const remove = useMutation({
    mutationFn: () => api.remove(sink),
    meta: { toast: false },
    onSuccess: async () => {
      // Only the list is read again: reading the deleted sink would answer 404.
      await queryClient.invalidateQueries({ queryKey: sinksKey(project), exact: true });
      void navigate({ to: "/projects/$project/sinks", params: { project } });
    },
  });
  const disable = useMutation({
    mutationFn: () => api.disable(sink),
    meta: { toast: false },
    onSuccess: async (saved) => {
      remember(saved);
      setDisabling(false);
    },
  });
  const name = sink.name;
  const { dead_letters: deadLetters, gaps } = sink;
  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-6 p-6">
      <PageHeader
        title={sink.name}
        description={<span>{kindName(i18n, sink.kind)}</span>}
        actions={
          <>
            {sink.enabled ? (
              <Button variant="outline" onClick={() => setDisabling(true)}>
                <PowerOffIcon aria-hidden />
                <Trans>Disable</Trans>
              </Button>
            ) : (
              <Button onClick={() => setEnabling(true)}>
                <PlayIcon aria-hidden />
                <Trans>Enable</Trans>
              </Button>
            )}
            <Button variant="outline" onClick={() => setSampling(true)}>
              <SendIcon aria-hidden />
              <Trans>Send sample</Trans>
            </Button>
            {sink.kind === "webhook" ? (
              <Button variant="outline" onClick={() => setRotating(true)}>
                <KeyRoundIcon aria-hidden />
                <Trans>Rotate secret</Trans>
              </Button>
            ) : null}
            <Button variant="outline" onClick={() => setDeleting(true)}>
              <Trash2Icon aria-hidden />
              <Trans>Delete</Trans>
            </Button>
          </>
        }
      />
      <dl className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
        <Fact label={<Trans>Health</Trans>}>
          <SinkHealthBadge sink={sink} />
        </Fact>
        <Fact label={<Trans>Enabled</Trans>}>{sink.enabled ? t`Yes` : t`No`}</Fact>
        <Fact label={<Trans>Lag</Trans>}>
          {sink.lag_seconds === null ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            formatLag(i18n, sink.lag_seconds)
          )}
        </Fact>
        <Fact label={<Trans>At risk</Trans>}>
          <SinkAtRisk sink={sink} />
        </Fact>
      </dl>
      <SinkAlerts project={project} sink={sink} onResumed={remember} />
      <Tabs
        label={t`Sink`}
        tabs={[
          {
            value: "settings",
            label: <Trans>Settings</Trans>,
            content: (
              <div className="flex flex-col gap-6">
                {sink.warnings.length > 0 ? (
                  <section className="flex flex-col gap-1 text-sm">
                    <h2 className="font-medium">
                      <Trans>What the last check found</Trans>
                    </h2>
                    <ul className="list-disc pl-5 text-muted-foreground">
                      {sink.warnings.map((warning) => (
                        <li key={warning}>{warning}</li>
                      ))}
                    </ul>
                  </section>
                ) : null}
                <SinkStreams sink={sink} />
                {/* A new version starts the form over from what was saved. */}
                <SinkEditor
                  key={sink.version}
                  project={project}
                  sink={sink}
                  onSaved={(saved) => {
                    remember(saved);
                    const { name: savedName } = saved;
                    toast.success(t`${savedName} is saved.`);
                  }}
                />
              </div>
            ),
          },
          {
            value: "dead-letters",
            label:
              sink.dead_letters > 0 ? (
                <Trans>Dead letters ({deadLetters})</Trans>
              ) : (
                <Trans>Dead letters</Trans>
              ),
            content: <DeadLettersTab project={project} sink={sink} />,
          },
          {
            value: "replay",
            label: <Trans>Replay</Trans>,
            content: <ReplayTab project={project} sink={sink} onReplayed={remember} />,
          },
          {
            value: "gaps",
            label: sink.gaps > 0 ? <Trans>Gaps ({gaps})</Trans> : <Trans>Gaps</Trans>,
            content: <GapsTab project={project} sink={sink} />,
          },
        ]}
      />
      <EnableSinkDialog
        project={project}
        sink={sink}
        open={enabling}
        onOpenChange={setEnabling}
        onEnabled={(enabled) => {
          remember(enabled);
          setEnabling(false);
          toast.success(t`${name} is enabled.`);
        }}
      />
      <SampleSinkDialog project={project} sink={sink} open={sampling} onOpenChange={setSampling} />
      <ConfirmDialog
        open={disabling}
        onOpenChange={(open) => {
          if (!open) {
            disable.reset();
            setDisabling(false);
          }
        }}
        title={t`Disable ${name}`}
        confirmLabel={<Trans>Disable sink</Trans>}
        pending={disable.isPending}
        refusal={disable.error}
        onConfirm={() => disable.mutate()}
      >
        <p>
          <Trans>
            The delivery in progress finishes and nothing new starts. The sink keeps its place in
            the event stream, so enabling it again continues where it stopped.
          </Trans>
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        open={rotating}
        onOpenChange={(open) => {
          if (!open) {
            rotate.reset();
            setRotating(false);
          }
        }}
        title={t`Rotate the secret of ${name}`}
        confirmLabel={<Trans>Rotate secret</Trans>}
        pending={rotate.isPending}
        refusal={rotate.error}
        onConfirm={() =>
          rotate.mutate(undefined, {
            onSuccess: ({ secret: issued, ...rotated }) => {
              setRotating(false);
              setSecret(issued);
              // The secret lives in the dialog alone: not in the cache, and
              // not in the finished mutation this screen would keep.
              remember(rotated as Sink);
              rotate.reset();
            },
          })
        }
      >
        <p>
          <Trans>
            A new secret signs from now on. The one it replaces keeps signing beside it for a day,
            so the receiver can switch to the new secret without refusing a batch.
          </Trans>
        </p>
      </ConfirmDialog>
      <SinkSecretDialog secret={secret} rotated onClose={() => setSecret(undefined)} />
      <ConfirmDialog
        open={deleting}
        onOpenChange={(open) => {
          if (!open) {
            remove.reset();
            setDeleting(false);
          }
        }}
        title={t`Delete ${name}`}
        confirmLabel={<Trans>Delete sink</Trans>}
        destructive
        pending={remove.isPending}
        refusal={remove.error}
        onConfirm={() => remove.mutate()}
      >
        <p>
          <Trans>
            Nothing more is delivered to it, and its place in the event stream is forgotten. This
            cannot be undone.
          </Trans>
        </p>
      </ConfirmDialog>
    </div>
  );
}

function Fact({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="text-muted-foreground">{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}
