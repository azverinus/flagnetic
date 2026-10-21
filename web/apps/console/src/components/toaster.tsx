import { Toaster as Sonner } from "sonner";

import { useTheme } from "@/lib/use-theme";

/**
 * Where toasts appear: bottom right, above the bar a form shows while it
 * holds unsaved changes, in the member's theme.
 */
export function Toaster() {
  const [theme] = useTheme();
  return (
    <Sonner
      theme={theme}
      position="bottom-right"
      offset={{ bottom: "var(--toast-offset, 16px)" }}
      richColors
      closeButton
    />
  );
}
