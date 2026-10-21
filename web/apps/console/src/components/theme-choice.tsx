import { Trans, useLingui } from "@lingui/react/macro";
import { useId } from "react";

import { type Theme, themes } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

/**
 * The theme, as radio buttons: the same choice as the personal menu's,
 * kept in this browser so the sign-in page shows it too.
 */
export function ThemeChoice() {
  const { t } = useLingui();
  const [theme, setTheme] = useTheme();
  const hint = useId();
  const names: Record<Theme, string> = {
    light: t`Light`,
    dark: t`Dark`,
    // Told apart from the audit log's actor of the same name, which reads differently in Russian.
    system: t({ message: "System", context: "theme" }),
  };

  return (
    <fieldset aria-describedby={hint} className="flex flex-col gap-3">
      <legend className="text-sm font-medium">
        <Trans>Theme</Trans>
      </legend>
      <p id={hint} className="text-sm text-muted-foreground">
        <Trans>
          Kept in this browser, and used on the sign-in page too. System follows your device's
          setting.
        </Trans>
      </p>
      <div className="flex flex-wrap gap-4">
        {themes.map((value) => (
          <label key={value} className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="theme"
              value={value}
              checked={theme === value}
              onChange={() => setTheme(value)}
              className="size-4 accent-primary"
            />
            {names[value]}
          </label>
        ))}
      </div>
    </fieldset>
  );
}
