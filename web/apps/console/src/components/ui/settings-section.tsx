import type { ReactNode } from "react";

export type SettingsSectionProps = {
  title: ReactNode;
  /** A sentence under the title saying what the section holds or does. */
  description?: ReactNode;
  children: ReactNode;
};

/** One section of a settings page: its heading, what it is for, then its content. */
export function SettingsSection({ title, description, children }: SettingsSectionProps) {
  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-lg font-semibold">{title}</h2>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {children}
    </section>
  );
}
