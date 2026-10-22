import { useNavigate, useSearch } from "@tanstack/react-router";
import { Tabs as TabsPrimitive } from "radix-ui";
import type { ReactNode } from "react";

import { cn } from "@/lib/cn";

export type Tab = {
  /** The value the search parameter takes for this tab. */
  value: string;
  label: ReactNode;
  content: ReactNode;
};

export type TabsProps = {
  /**
   * The search parameter that holds the open tab, so a link opens it.
   * Switching replaces the history entry, so Back leaves the page rather
   * than stepping through its tabs.
   */
  param?: string;
  tabs: readonly Tab[];
  /** What the row of tabs is called for a screen reader. */
  label: string;
  className?: string;
  /**
   * The open tab and how to open another, for a screen whose tabs are not
   * one search parameter, such as a flag's environments beside its
   * history; `param` is then unused.
   */
  value?: string;
  onValueChange?: (value: string) => void;
};

/**
 * Tabs whose open tab is a search parameter of the address. The first tab
 * is open when the parameter is absent or names no tab, and opening it
 * removes the parameter, so the plain address is the first tab. The row
 * scrolls sideways rather than widen the page; one line marks the open
 * tab. Arrow keys move between tabs, as screen readers announce them.
 */
export function Tabs({
  param = "tab",
  tabs,
  label,
  className,
  value: controlled,
  onValueChange,
}: TabsProps) {
  const search = useSearch({ strict: false }) as Record<string, unknown>;
  const navigate = useNavigate();
  const first = tabs[0]?.value ?? "";
  const asked = controlled ?? search[param];
  const value = tabs.some((tab) => tab.value === asked) ? (asked as string) : first;

  return (
    <TabsPrimitive.Root
      value={value}
      onValueChange={(next) => {
        if (onValueChange) {
          onValueChange(next);
          return;
        }
        void navigate({
          to: ".",
          search: (prev: Record<string, unknown>) => ({
            ...prev,
            [param]: next === first ? undefined : next,
          }),
          replace: true,
        } as never);
      }}
      className={cn("flex min-w-0 flex-col gap-4", className)}
    >
      <div className="min-w-0 overflow-x-auto border-b">
        <TabsPrimitive.List aria-label={label} className="flex w-max gap-1">
          {tabs.map((tab) => (
            <TabsPrimitive.Trigger
              key={tab.value}
              value={tab.value}
              className="-mb-px border-b-2 border-transparent px-3 py-2 text-sm whitespace-nowrap text-muted-foreground hover:text-foreground data-[state=active]:border-primary data-[state=active]:font-medium data-[state=active]:text-foreground"
            >
              {tab.label}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>
      </div>
      {tabs.map((tab) => (
        <TabsPrimitive.Content key={tab.value} value={tab.value} className="min-w-0">
          {tab.content}
        </TabsPrimitive.Content>
      ))}
    </TabsPrimitive.Root>
  );
}
