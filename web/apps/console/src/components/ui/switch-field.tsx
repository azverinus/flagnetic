import { type ReactNode, useId } from "react";

import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";

export type SwitchFieldProps = {
  label: ReactNode;
  /** What switching it on does, and when to. */
  hint: ReactNode;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  disabled?: boolean;
};

/**
 * A setting that is on or off: the label and what it means on the left,
 * the switch on the right, wired so a screen reader names the switch by
 * the label and reads the meaning with it.
 */
export function SwitchField({
  label,
  hint,
  checked,
  onCheckedChange,
  disabled = false,
}: SwitchFieldProps) {
  const id = useId();
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="flex flex-col gap-1">
        <Label htmlFor={id}>{label}</Label>
        <p id={`${id}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      </div>
      <Switch
        id={id}
        aria-describedby={`${id}-hint`}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
      />
    </div>
  );
}
