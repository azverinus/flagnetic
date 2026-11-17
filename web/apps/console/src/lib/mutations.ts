import { type QueryKey, useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

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
 *
 * A change whose answer carries a secret shown once says so with
 * `reveals`, which implies `secret`: `onDone` then also gets the
 * variables and runs as soon as the answer arrives, before the list is
 * read again, so the secret reaches the screen that shows it even when
 * reading the list is slow, fails, or replaces the form that asked.
 */
export function useListMutation<Variables, Data = unknown>(
  mutationFn: (variables: Variables) => Promise<Data>,
  list: QueryKey,
  onDone: (data: Data, variables: Variables) => void,
  { secret = false, reveals = false }: { secret?: boolean; reveals?: boolean } = {},
) {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn,
    ...(secret || reveals ? forgetsSecret : {}),
    meta: { toast: false },
    onSuccess: async (data: Data, variables: Variables) => {
      if (reveals) onDone(data, variables);
      await queryClient.invalidateQueries({ queryKey: list });
      // Not given the variables unless it reveals: most callers take the answer alone.
      if (!reveals) (onDone as (data: Data) => void)(data);
    },
  });
  // The answer was delivered by `onDone`; the finished mutation, which
  // would keep it, is reset at once.
  const { isSuccess, reset } = mutation;
  useEffect(() => {
    if (reveals && isSuccess) reset();
  }, [reveals, isSuccess, reset]);
  return mutation;
}
