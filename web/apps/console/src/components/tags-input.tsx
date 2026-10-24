import { useLingui } from "@lingui/react/macro";
import { XIcon } from "lucide-react";
import { type KeyboardEvent, type Ref, useState } from "react";

import { cn } from "@/lib/cn";

export type TagsInputProps = {
  value: readonly string[];
  onChange: (tags: string[]) => void;
  /** The text input, so a form can focus the field on an error. */
  ref?: Ref<HTMLInputElement>;
  /** Called once the text input loses focus, after what was typed is added. */
  onBlur?: () => void;
  /** Whether the tags can no longer change: nothing is added or removed. */
  disabled?: boolean;
  /** Passed to the text input, so a `Field` names and describes it. */
  id?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
};

/**
 * A list of tags typed one at a time: Enter or a comma adds what is
 * typed, as does leaving the field, so a tag typed and not yet added is
 * never lost; a blank or repeated tag is not added, and Enter never
 * submits the form the field is in. Each tag carries a button that
 * removes it.
 */
export function TagsInput({ value, onChange, onBlur, disabled, ...input }: TagsInputProps) {
  const { t } = useLingui();
  const [typed, setTyped] = useState("");

  const add = () => {
    const tag = typed.trim();
    setTyped("");
    if (tag !== "" && !value.includes(tag)) onChange([...value, tag]);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    // Enter never submits the enclosing form, even with nothing typed:
    // leaving the field adds what was typed and empties it, so an Enter
    // pressed after focus came back (a closing menu returns focus to its
    // trigger, a person clicks back in) would otherwise submit the form
    // instead of finishing the tag.
    if (event.key === "Enter" || (event.key === "," && typed.trim() !== "")) {
      event.preventDefault();
      add();
    }
  };

  return (
    <div
      className={cn(
        "flex min-h-9 w-full flex-wrap items-center gap-1.5 rounded-md border border-input bg-background px-2 py-1 shadow-xs",
        input["aria-invalid"] && "border-destructive",
        disabled && "cursor-not-allowed opacity-50",
      )}
    >
      {value.length > 0 ? (
        <ul aria-label={t`Chosen tags`} className="flex flex-wrap gap-1.5">
          {value.map((tag) => (
            <li
              key={tag}
              className="flex items-center gap-1 rounded-sm bg-muted py-0.5 pr-0.5 pl-2 text-xs"
            >
              {tag}
              <button
                type="button"
                aria-label={t`Remove tag ${tag}`}
                disabled={disabled}
                className="rounded-sm p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                onClick={() => onChange(value.filter((other) => other !== tag))}
              >
                <XIcon aria-hidden className="size-3" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      <input
        {...input}
        autoComplete="off"
        disabled={disabled}
        className="h-7 min-w-24 flex-1 rounded-sm bg-transparent px-1 text-sm"
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
        onKeyDown={onKeyDown}
        onBlur={() => {
          add();
          onBlur?.();
        }}
      />
    </div>
  );
}
