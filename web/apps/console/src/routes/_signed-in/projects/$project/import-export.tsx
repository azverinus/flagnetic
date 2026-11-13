import { msg } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import { createFileRoute } from "@tanstack/react-router";

import { ExportProject } from "@/components/export-project";
import { ImportProject } from "@/components/import-project";
import { PageHeader } from "@/components/ui/page-header";
import { SettingsSection } from "@/components/ui/settings-section";
import { useCan } from "@/lib/access";

export const Route = createFileRoute("/_signed-in/projects/$project/import-export")({
  staticData: { crumb: msg`Import and export` },
  component: ImportExport,
});

/**
 * Where a project's configuration leaves and enters as a project
 * document. Every reader of the project exports; the sections that write
 * are listed to owners alone, since nobody else may import.
 */
function ImportExport() {
  const { t } = useLingui();
  const { project } = Route.useParams();
  const mayImport = useCan("project.import");
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-8 p-6">
      <PageHeader
        title={t`Import and export`}
        description={
          <Trans>
            Move the project's configuration as one project document, in JSON or YAML. Members,
            tokens, SDK keys, webhooks, change requests and the audit log are never in it.
          </Trans>
        }
      />
      <SettingsSection
        title={t`Export`}
        description={
          <Trans>
            Downloads the flags, segments, experiments, environments, unit types and attributes of
            the project, with the rule ids and salts that keep every unit in its bucket.
          </Trans>
        }
      >
        <ExportProject project={project} />
      </SettingsSection>
      {mayImport ? (
        <>
          <SettingsSection
            title={t`Import`}
            description={
              <Trans>
                Merges a project document into this project, or replaces the project's configuration
                with it. You see everything it would create, update and delete before you apply it.
              </Trans>
            }
          >
            <ImportProject project={project} />
          </SettingsSection>
          <SettingsSection title={t`Import from Firebase`}>
            <p className="text-sm text-muted-foreground">
              <Trans>Importing a Firebase Remote Config template is not available here yet.</Trans>
            </p>
          </SettingsSection>
        </>
      ) : null}
    </div>
  );
}
