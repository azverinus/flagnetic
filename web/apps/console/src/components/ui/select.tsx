import { CheckIcon, ChevronDownIcon } from "lucide-react";
import { Select as SelectPrimitive } from "radix-ui";
import type { ComponentProps, ReactNode } from "react";

import { cn } from "@/lib/cn";

export type SelectProps = Omit<ComponentProps<typeof SelectPrimitive.Root>, "children"> &
  Pick<
    ComponentProps<typeof SelectPrimitive.Trigger>,
    | "id"
    | "className"
    | "ref"
    | "onBlur"
    | "aria-describedby"
    | "aria-invalid"
    | "aria-label"
    | "aria-labelledby"
  > & {
    /** Shown while no option is chosen. */
    placeholder?: ReactNode;
    /** The `SelectItem`s to choose from. */
    children: ReactNode;
  };

/**
 * A choice among a few options, on Radix Select like every other popup of
 * the console, so it looks and behaves as the menus and popovers do. It is
 * one element: `Field` wires its label, hint and error to the trigger, the
 * button a screen reader announces as a combobox, and `ref` and `onBlur`
 * reach the trigger too, so react-hook-form's `Controller` can focus it on
 * an error. With a `name` inside a form, Radix adds a hidden native select
 * so a plain form submission carries the value. The open list is no wider
 * than the room beside the trigger and wraps a long option, so it never
 * reaches past the viewport.
 */
export function Select({
  id,
  className,
  ref,
  onBlur,
  placeholder,
  children,
  "aria-describedby": describedBy,
  "aria-invalid": invalid,
  "aria-label": ariaLabel,
  "aria-labelledby": labelledBy,
  ...root
}: SelectProps) {
  return (
    <SelectPrimitive.Root {...root}>
      <SelectPrimitive.Trigger
        id={id}
        ref={ref}
        onBlur={onBlur}
        aria-describedby={describedBy}
        aria-invalid={invalid}
        aria-label={ariaLabel}
        aria-labelledby={labelledBy}
        className={cn(
          "flex h-9 w-full items-center justify-between gap-2 rounded-md border border-input bg-background py-1 pr-2.5 pl-3 text-left text-sm shadow-xs transition-colors disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive data-placeholder:text-muted-foreground [&>span]:truncate",
          className,
        )}
      >
        <SelectPrimitive.Value placeholder={placeholder} />
        <SelectPrimitive.Icon asChild>
          <ChevronDownIcon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content
          position="popper"
          sideOffset={4}
          className="z-50 max-h-(--radix-select-content-available-height) max-w-(--radix-select-content-available-width) min-w-(--radix-select-trigger-width) overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md"
        >
          <SelectPrimitive.Viewport className="p-1">{children}</SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  );
}

/**
 * One option of a `Select`. Its value cannot be the empty string, which
 * Radix keeps for "nothing chosen" so the placeholder shows.
 */
export function SelectItem({
  className,
  children,
  ...props
}: ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      className={cn(
        "relative flex w-full cursor-default items-center rounded-sm py-1.5 pr-2 pl-8 text-sm break-words outline-hidden select-none focus:bg-accent focus:text-accent-foreground data-disabled:pointer-events-none data-disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <span className="absolute left-2 flex size-3.5 items-center justify-center">
        <SelectPrimitive.ItemIndicator>
          <CheckIcon aria-hidden className="size-4" />
        </SelectPrimitive.ItemIndicator>
      </span>
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
    </SelectPrimitive.Item>
  );
}
