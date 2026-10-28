import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation } from "@tanstack/react-query";
import { DownloadIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import type { AuditSearch } from "@/lib/audit";
import { type AuditFileScope, type AuditFormat, downloadAudit } from "@/lib/audit-file";

export type DownloadAuditProps = {
  scope: AuditFileScope;
  /** The filters applied now; the page is ignored. */
  search: AuditSearch;
};

/**
 * Downloads every entry the applied filters match, as JSON Lines or CSV.
 * A refusal, such as a filter matching more than the server's cap, is
 * toasted with the way out.
 */
export function DownloadAudit({ scope, search }: DownloadAuditProps) {
  const { t } = useLingui();
  const download = useMutation({
    mutationFn: (format: AuditFormat) => downloadAudit(scope, search, format),
    // A download changes nothing, so the logs shown stay as they are.
    meta: { readsAuditAgain: false },
  });
  const button = (format: AuditFormat, label: ReactNode) => (
    <Button variant="outline" disabled={download.isPending} onClick={() => download.mutate(format)}>
      <DownloadIcon aria-hidden />
      {label}
    </Button>
  );
  return (
    <div
      role="group"
      aria-label={t`Download the audit log`}
      className="flex flex-wrap justify-end gap-2"
    >
      {button("jsonl", <Trans>Download JSON Lines</Trans>)}
      {button("csv", <Trans>Download CSV</Trans>)}
    </div>
  );
}
