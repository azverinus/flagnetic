import { deleteProject, type Project } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";

import { Can } from "@/components/can";
import { DownloadAudit } from "@/components/download-audit";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ApiError, unwrap } from "@/lib/errors";
import { ifMatch } from "@/lib/http";
import { projectQuery, projectsQuery } from "@/lib/queries";

const unfiltered = {};

/**
 * The project's deletion, for whoever may delete it: the audit log offered
 * for download first, since the project's own screens go with it, and the
 * project's key typed out before the button works. Afterwards the member
 * lands on the list of projects.
 */
export function DeleteProject({ project }: { project: Project }) {
  const { t } = useLingui();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [asking, setAsking] = useState(false);
  const key = project.key;
  const remove = useMutation({
    mutationFn: () =>
      unwrap(deleteProject({ path: { project: key }, headers: ifMatch(project.version) })),
    meta: { toast: false },
    onSuccess: async () => {
      setAsking(false);
      toast.success(t`The project ${key} is deleted.`);
      // The whole list is asked for: the last project would otherwise be
      // reopened from the cached list, which still holds this one.
      await navigate({ to: "/", search: { all: true } });
      queryClient.removeQueries({ queryKey: projectQuery(key).queryKey });
      await queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey, exact: true });
    },
    onError: (error) => {
      // A newer version is read so that confirming again is based on it.
      if (error instanceof ApiError && error.code === "version_mismatch") {
        void queryClient.invalidateQueries({ queryKey: projectQuery(key).queryKey, exact: true });
      }
    },
  });

  return (
    <section
      aria-labelledby="delete-project"
      className="flex flex-col gap-3 rounded-lg border border-destructive/40 p-4"
    >
      <h2 id="delete-project" className="text-base font-semibold">
        <Trans>Delete this project</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          Deleting removes the project with its flags, segments, experiments, environments and SDK
          keys at once, and its key becomes free. Its audit entries stay readable in the instance's
          audit log.
        </Trans>
      </p>
      <div>
        <Can permission="project.delete" whenMissing="disable">
          <Button variant="destructive" onClick={() => setAsking(true)}>
            <Trans>Delete project</Trans>
          </Button>
        </Can>
      </div>
      <ConfirmDialog
        open={asking}
        onOpenChange={(open) => {
          if (!open) {
            remove.reset();
            setAsking(false);
          }
        }}
        title={t`Delete ${key}`}
        confirmLabel={<Trans>Delete project</Trans>}
        destructive
        typeToConfirm={key}
        pending={remove.isPending}
        refusal={remove.error}
        onConfirm={() => remove.mutate()}
      >
        <p>
          <Trans>
            Everything in the project is deleted at once, its SDK keys stop working, and this cannot
            be undone.
          </Trans>
        </p>
        <p>
          <Trans>
            Download the audit log first if you want to keep it: it is kept on the instance, but
            only an instance admin can read it there.
          </Trans>
        </p>
        <DownloadAudit scope={{ project: key }} search={unfiltered} />
      </ConfirmDialog>
    </section>
  );
}
