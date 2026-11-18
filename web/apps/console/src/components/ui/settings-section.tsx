import type { ReactNode } from "react";

import { Card, CardContent, CardHeader } from "./card";

export type SettingsSectionProps = {
  title: ReactNode;
  /** A sentence under the title saying what the section holds or does. */
  description?: ReactNode;
  /** Controls at the end of the heading. */
  actions?: ReactNode;
  children: ReactNode;
};

/** One section of a settings page, as a card: its heading, what it is for, then its content. */
export function SettingsSection({ title, description, actions, children }: SettingsSectionProps) {
  return (
    <Card>
      <CardHeader title={title} description={description} actions={actions} />
      <CardContent className="pt-4">{children}</CardContent>
    </Card>
  );
}
