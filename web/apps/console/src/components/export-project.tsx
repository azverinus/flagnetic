import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation } from "@tanstack/react-query";
import { DownloadIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Select, SelectItem } from "@/components/ui/select";
import {
  type DocumentFormat,
  documentFormats,
  downloadProjectDocument,
} from "@/lib/project-document";

/**
 * Downloads the project document, JSON or YAML, as the server serves it.
 * A refusal is toasted by the console; the button is free again after it.
 */
export function ExportProject({ project }: { project: string }) {
  const { t } = useLingui();
  const [format, setFormat] = useState<DocumentFormat>("json");
  const download = useMutation({
    mutationFn: () => downloadProjectDocument(project, format),
    // A download changes nothing, so the logs shown stay as they are.
    meta: { readsAuditAgain: false },
  });
  return (
    <div className="flex flex-wrap items-end gap-3">
      <Field label={t`Document format`} className="w-40">
        <Select value={format} onValueChange={(value) => setFormat(value as DocumentFormat)}>
          {documentFormats.map((option) => (
            <SelectItem key={option} value={option}>
              {option === "json" ? "JSON" : "YAML"}
            </SelectItem>
          ))}
        </Select>
      </Field>
      <Button disabled={download.isPending} onClick={() => download.mutate()}>
        <DownloadIcon aria-hidden />
        <Trans>Export</Trans>
      </Button>
    </div>
  );
}
