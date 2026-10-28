import { type QueryKey, useMutation, useQueryClient } from "@tanstack/react-query";

/**
 * The options of a mutation whose variables or answer carry a secret: a
 * password, an invitation link or a token's value. The mutation cache
 * keeps a finished mutation, variables and answer included, for five
 * minutes after its screen resets it or goes away; with no time at all
 * it is dropped then, so the secret lives only where the screen shows it.
 */
export const forgetsSecret = { gcTime: 0 } as const;

/**
 * A change to one row of a list shown on the screen, such as creating,
 * editing or deleting an environment: its refusal is explained where it
 * was made, not toasted, and once it succeeds the list is read again
 * before `onDone`, so a closing dialog already shows the change. One
 * whose variables or answer carry a secret says so with `secret`, and is
 * then kept as {@link forgetsSecret} says. `onDone` is given the answer
 * and, unlike a callback of `mutate`, runs even when reading the list
 * again replaced the screen that made the change.
 */
export function useListMutation<Variables, Data = unknown>(
  mutationFn: (variables: Variables) => Promise<Data>,
  list: QueryKey,
  onDone: (data: Data) => void,
  { secret = false }: { secret?: boolean } = {},
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    ...(secret ? forgetsSecret : {}),
    meta: { toast: false },
    onSuccess: async (data: Data) => {
      await queryClient.invalidateQueries({ queryKey: list });
      onDone(data);
    },
  });
}
