import { useEffect, useState } from "react";

/** The longest delay a browser timer holds; a longer one fires at once. */
const longestTimer = 2 ** 31 - 1;

/**
 * The current time, moved on as each of instants passes, so whatever is
 * judged against it, such as whether a token has expired, changes on the
 * page at that moment rather than at the next reload. Instants already past
 * or null never move it; a timer firing early only schedules itself again.
 */
export function useNowPassing(instants: readonly (string | null)[]): Date {
  const [now, setNow] = useState(() => new Date());
  let next = Number.POSITIVE_INFINITY;
  for (const instant of instants) {
    const at = instant === null ? Number.NaN : Date.parse(instant);
    if (at > now.getTime() && at < next) next = at;
  }
  useEffect(() => {
    if (!Number.isFinite(next)) return;
    const timer = setTimeout(
      () => setNow(new Date()),
      Math.min(Math.max(next - Date.now(), 0), longestTimer),
    );
    return () => clearTimeout(timer);
  }, [next]);
  return now;
}
