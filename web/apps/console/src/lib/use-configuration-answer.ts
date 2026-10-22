import type { Flag, FlagConfiguration } from "@flagnetic/api";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";

import { withConfiguration } from "./flag-detail";
import { flagQuery, flagsQueryKey } from "./queries";

/**
 * What a page does with a configuration the server answered a write
 * with: the flag it shows takes it in place of the one it had, so the
 * page shows the write without reading the flag again, and the flags
 * list, whose flag states it may change, is read again when next shown.
 */
export function useConfigurationAnswer(
  project: string,
  flag: string,
): (configuration: FlagConfiguration) => void {
  const queryClient = useQueryClient();
  return useCallback(
    (configuration: FlagConfiguration) => {
      queryClient.setQueryData(flagQuery(project, flag).queryKey, (current: Flag | undefined) =>
        current ? withConfiguration(current, configuration) : current,
      );
      void queryClient.invalidateQueries({ queryKey: [...flagsQueryKey(project), "list"] });
    },
    [queryClient, project, flag],
  );
}
