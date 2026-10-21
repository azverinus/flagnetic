import { Trans, useLingui } from "@lingui/react/macro";
import { useEffect, useRef, useState } from "react";

import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";

/** How long typing pauses before the search is applied. */
const searchPause = 250;

export type SearchFieldProps = {
  /** The search in force, as the address holds it. */
  value: string;
  /** Applies a search, once typing pauses. */
  onChange: (q: string) => void;
};

/**
 * The search by name or email. It is applied once typing pauses, so the
 * address and the list follow without a request for every keystroke.
 */
export function SearchField({ value, onChange }: SearchFieldProps) {
  const { t } = useLingui();
  const [typed, setTyped] = useState(value);
  // A search the address changes on its own, going back or following a
  // link, replaces what was typed, so the pause never writes the old
  // search back over it. What was typed and already applied is kept as
  // typed, with its spaces.
  const [applied, setApplied] = useState(value);
  if (value !== applied) {
    setApplied(value);
    if (typed.trim() !== value) setTyped(value);
  }
  const changed = useRef(onChange);
  useEffect(() => {
    changed.current = onChange;
  });
  useEffect(() => {
    if (typed.trim() === value) return;
    const timer = setTimeout(() => changed.current(typed), searchPause);
    return () => clearTimeout(timer);
  }, [typed, value]);
  return (
    <Field label={<Trans>Search</Trans>} className="w-72 max-w-full">
      <Input
        type="search"
        placeholder={t`Name or email`}
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
      />
    </Field>
  );
}
