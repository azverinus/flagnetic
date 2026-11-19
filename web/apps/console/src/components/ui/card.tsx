import { type ComponentProps, createContext, type ReactNode, useContext } from "react";

import { cn } from "@/lib/cn";

// Whether something renders on a card, which already gives it a surface.
const OnCard = createContext(false);

/** Whether the caller renders inside a `Card`. */
export function useOnCard() {
  return useContext(OnCard);
}

/**
 * The only section surface: a bordered, rounded block on the card
 * colour. A list's card keeps a minimum height of 40% of the viewport
 * (`list`), so a short or empty list does not shrink the page to a strip.
 */
export function Card({
  list = false,
  className,
  ...props
}: ComponentProps<"section"> & { list?: boolean }) {
  return (
    <OnCard.Provider value>
      <section
        {...props}
        className={cn(
          "flex min-w-0 flex-col rounded-xl border bg-card text-card-foreground shadow-xs",
          list && "min-h-[40dvh] overflow-clip",
          className,
        )}
      />
    </OnCard.Provider>
  );
}

export type CardHeaderProps = {
  title: ReactNode;
  /** The heading's id, for a card that names itself by its title (`aria-labelledby`). */
  titleId?: string;
  description?: ReactNode;
  /** Controls for the section, at the end of its heading. */
  actions?: ReactNode;
  className?: string;
};

/** A card's heading: its title, a sentence on what it holds, and its actions. */
export function CardHeader({ title, titleId, description, actions, className }: CardHeaderProps) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-start justify-between gap-x-4 gap-y-2 p-5 pb-0 max-sm:p-4 max-sm:pb-0",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <h2 id={titleId} className="text-base/6 font-medium">
          {title}
        </h2>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

/** A card's body. `flush` lets a table or list run to the card's edges. */
export function CardContent({
  flush = false,
  className,
  ...props
}: ComponentProps<"div"> & { flush?: boolean }) {
  return (
    <div
      {...props}
      className={cn(
        "flex min-w-0 flex-1 flex-col gap-4",
        flush ? "pt-4" : "p-5 max-sm:p-4",
        className,
      )}
    />
  );
}
