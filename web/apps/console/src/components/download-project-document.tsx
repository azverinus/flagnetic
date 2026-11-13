import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation } from "@tanstack/react-query";
import { DownloadIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { type DocumentFormat, downloadProjectDocument } from "@/lib/project-document";

/**
 * Two buttons that download the project document in JSON or YAML, for a
 * place where choosing a format would be one step too many. A refusal is
 * toasted by the console.
 */
export function DownloadProjectDocument({ project }: { project: string }) {
  const { t } = useLingui();
  const download = useMutation({
    mutationFn: (format: DocumentFormat) => downloadProjectDocument(project, format),
    // A download changes nothing, so the logs shown stay as they are.
    meta: { readsAuditAgain: false },
  });
  return (
    <div
      role="group"
      aria-label={t`Export the project's configuration`}
      className="flex flex-wrap justify-end gap-2"
    >
      <Button
        variant="outline"
        disabled={download.isPending}
        onClick={() => download.mutate("json")}
      >
        <DownloadIcon aria-hidden />
        <Trans>Export as JSON</Trans>
      </Button>
      <Button
        variant="outline"
        disabled={download.isPending}
        onClick={() => download.mutate("yaml")}
      >
        <DownloadIcon aria-hidden />
        <Trans>Export as YAML</Trans>
      </Button>
    </div>
  );
}
