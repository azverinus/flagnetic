import { Trans } from "@lingui/react/macro";
import { useMutation } from "@tanstack/react-query";
import { DownloadIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { downloadRuleset } from "@/lib/ruleset-file";

export type DownloadRulesetProps = {
  project: string;
  environment: string;
};

/**
 * Saves the environment's ruleset, the document a server SDK holds, as a
 * file for a test helper or an offline evaluation. A refusal is toasted.
 */
export function DownloadRuleset({ project, environment }: DownloadRulesetProps) {
  const download = useMutation({ mutationFn: () => downloadRuleset(project, environment) });
  return (
    <Button variant="outline" disabled={download.isPending} onClick={() => download.mutate()}>
      <DownloadIcon aria-hidden />
      <Trans>Download ruleset</Trans>
    </Button>
  );
}
