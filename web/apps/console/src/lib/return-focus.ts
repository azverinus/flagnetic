import { useRef } from "react";

/**
 * Gives focus back to whatever had it when a dialog opened. A dialog
 * opened from its own trigger gets this from Radix; one opened by state,
 * from a menu item or after a form, would otherwise leave focus on the
 * page's body.
 */
export function useReturnFocus() {
  const opener = useRef<HTMLElement | null>(null);
  return {
    onOpenAutoFocus: () => {
      opener.current =
        document.activeElement instanceof HTMLElement ? document.activeElement : null;
    },
    onCloseAutoFocus: (event: Event) => {
      const element = opener.current;
      opener.current = null;
      if (element?.isConnected) {
        event.preventDefault();
        element.focus();
      }
    },
  };
}
