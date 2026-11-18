import { Trans, useLingui } from "@lingui/react/macro";
import { XIcon } from "lucide-react";
import type { ReactNode } from "react";

import { SearchField } from "@/components/search-field";
import { Button } from "@/components/ui/button";
import { ContextPanelOpener } from "@/components/ui/context-panel";
import { Select, SelectItem } from "@/components/ui/select";

/** A facet in force, as a chip in the toolbar. */
export type ToolbarChip = {
  /** What identifies the facet to `onRemove`. */
  id: string;
  /** The facet and its choice in words, such as "Type: String". */
  label: string;
};

export type ToolbarProps = {
  search?: {
    value: string;
    onChange: (q: string) => void;
    placeholder: string;
  };
  sort?: {
    value: string;
    options: readonly FacetSortOption[];
    onChange: (value: string) => void;
  };
  chips: readonly ToolbarChip[];
  onRemoveChip: (id: string) => void;
  onClearAll: () => void;
  /** Controls at the toolbar's end, such as a download. */
  actions?: ReactNode;
};

export type FacetSortOption = { value: string; label: string };

/**
 * A list's one-row toolbar: search and sort, the button that brings the
 * facets back while their panel is folded away, and a removable chip for
 * each facet in force with "Clear all" after them. The chips are how the
 * facets stay visible whatever the state of the panel.
 */
export function Toolbar({ search, sort, chips, onRemoveChip, onClearAll, actions }: ToolbarProps) {
  const { t } = useLingui();
  return (
    <div className="flex flex-wrap items-center gap-2">
      {search ? (
        <SearchField
          hideLabel
          value={search.value}
          onChange={search.onChange}
          placeholder={search.placeholder}
        />
      ) : null}
      {sort ? (
        <Select
          aria-label={t`Sort by`}
          className="w-44"
          value={sort.value}
          onValueChange={sort.onChange}
        >
          {sort.options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </Select>
      ) : null}
      <ContextPanelOpener>
        <Trans>Filters</Trans>
      </ContextPanelOpener>
      {chips.length > 0 ? (
        <ul aria-label={t`Filters in force`} className="flex flex-wrap items-center gap-1.5">
          {chips.map((chip) => {
            const { label } = chip;
            return (
              <li
                key={chip.id}
                className="flex h-7 items-center gap-1 rounded-md border bg-card pr-0.5 pl-2 text-xs"
              >
                <span className="truncate">{chip.label}</span>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-6"
                  aria-label={t`Remove the filter ${label}`}
                  onClick={() => onRemoveChip(chip.id)}
                >
                  <XIcon aria-hidden className="size-3.5" />
                </Button>
              </li>
            );
          })}
        </ul>
      ) : null}
      {chips.length > 0 ? (
        <Button variant="ghost" size="sm" onClick={onClearAll}>
          <Trans>Clear all</Trans>
        </Button>
      ) : null}
      {actions ? <div className="ml-auto flex items-center gap-2">{actions}</div> : null}
    </div>
  );
}
