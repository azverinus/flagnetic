import { type QueryKey, useMutation, useQueryClient } from "@tanstack/react-query";

/**
 * A change to one row of a list shown on the screen, such as creating,
 * editing or deleting an environment: its refusal is explained where it
 * was made, not toasted, and once it succeeds the list is read again
 * before `onDone`, so a closing dialog already shows the change.
 */
export function useListMutation<Variables>(
  mutationFn: (variables: Variables) => Promise<unknown>,
  list: QueryKey,
  onDone: () => void,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    meta: { toast: false },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: list });
      onDone();
    },
  });
}
