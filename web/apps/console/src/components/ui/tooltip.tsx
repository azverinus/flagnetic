import {
  Children,
  cloneElement,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  useId,
  useState,
} from "react";

type DescribedProps = { "aria-describedby"?: string };

export type TooltipProps = {
  /** What the tooltip says; it also becomes the element's description. */
  content: ReactNode;
  /** The one element it describes. */
  children: ReactElement<DescribedProps>;
};

/**
 * A short text about one element: shown while the pointer is over it or
 * it has focus, put away by Escape, and always its accessible description,
 * so a screen reader hears it without hovering. Hovering is tracked on a
 * wrapper, so an element that takes no pointer events still shows it.
 */
export function Tooltip({ content, children }: TooltipProps) {
  const id = useId();
  const [shown, setShown] = useState(false);
  const element = Children.only(children);
  const dismiss = (event: KeyboardEvent) => {
    if (event.key === "Escape" && shown) {
      event.stopPropagation();
      setShown(false);
    }
  };

  return (
    <span
      className="relative inline-flex"
      onPointerEnter={() => setShown(true)}
      onPointerLeave={() => setShown(false)}
      onFocus={() => setShown(true)}
      onBlur={() => setShown(false)}
      onKeyDown={dismiss}
    >
      {cloneElement(element, {
        "aria-describedby": [element.props["aria-describedby"], id].filter(Boolean).join(" "),
      })}
      <span
        id={id}
        role="tooltip"
        hidden={!shown}
        className="absolute top-full right-0 z-50 mt-1 w-max max-w-xs rounded-md border bg-popover px-2.5 py-1.5 text-xs font-normal text-popover-foreground shadow-md"
      >
        {content}
      </span>
    </span>
  );
}
