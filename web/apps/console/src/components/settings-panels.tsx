import { useLingui } from "@lingui/react/macro";
import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";

import {
  instanceGroupLabels,
  instanceGroupOrder,
  instanceScreens,
  openableSettingsScreens,
  settingsGroupLabels,
  settingsGroupOrder,
} from "@/components/area-screens";
import { SinkAttentionMarker } from "@/components/sink-banner";
import { ContextPanel, PanelGroup, PanelLink } from "@/components/ui/context-panel";
import { WebhookAttentionMarker } from "@/components/webhook-disabled-banner";
import { meQuery, projectMeQuery } from "@/lib/queries";

/**
 * The screens of a project's settings, in four groups, each listed by the
 * permission it has today. Named "Settings" so that "Project" still names
 * exactly one navigation in the window: the sidebar's. The attention of
 * the sinks and the webhooks shows beside their links, as it does on the
 * sidebar's link to the area.
 */
export function ProjectSettingsPanel() {
  const { i18n, t } = useLingui();
  const { project } = useParams({ strict: false });
  const me = useQuery(projectMeQuery(project ?? ""));
  if (project === undefined || me.data === undefined) return null;
  const screens = openableSettingsScreens(new Set(me.data.permissions));
  return (
    <ContextPanel label={t`Settings`}>
      {settingsGroupOrder.map((group) => {
        const inGroup = screens.filter((screen) => screen.group === group);
        if (inGroup.length === 0) return null;
        return (
          <PanelGroup key={group} label={i18n._(settingsGroupLabels[group])}>
            {inGroup.map((screen) => (
              <PanelLink
                key={screen.to}
                to={screen.to}
                params={{ project }}
                attention={
                  screen.to === "/projects/$project/sinks" ? (
                    <SinkAttentionMarker project={project} />
                  ) : screen.to === "/projects/$project/webhooks" ? (
                    <WebhookAttentionMarker project={project} />
                  ) : undefined
                }
              >
                {i18n._(screen.label)}
              </PanelLink>
            ))}
          </PanelGroup>
        );
      })}
    </ContextPanel>
  );
}

/**
 * The instance's screens for an instance admin: Workspace, Sign-in and
 * Delivery, then the audit log on its own. Anyone else sees none, since
 * the route tells them the area is not theirs.
 */
export function InstancePanel() {
  const { i18n, t } = useLingui();
  const me = useQuery(meQuery);
  if (!me.data?.member?.instance_admin) return null;
  return (
    <ContextPanel label={t`Instance`}>
      {instanceGroupOrder.map((group) => (
        <PanelGroup key={group} label={i18n._(instanceGroupLabels[group])}>
          {instanceScreens
            .filter((screen) => screen.group === group)
            .map((screen) => (
              <PanelLink key={screen.to} to={screen.to}>
                {i18n._(screen.label)}
              </PanelLink>
            ))}
        </PanelGroup>
      ))}
      <PanelGroup>
        {instanceScreens
          .filter((screen) => screen.group === undefined)
          .map((screen) => (
            <PanelLink key={screen.to} to={screen.to}>
              {i18n._(screen.label)}
            </PanelLink>
          ))}
      </PanelGroup>
    </ContextPanel>
  );
}

/** The member's own account: who they are, how they sign in, and their tokens. */
export function PersonalSettingsPanel() {
  const { t } = useLingui();
  return (
    <ContextPanel label={t`Personal settings`}>
      <PanelGroup>
        <PanelLink to="/settings/profile">{t`Profile`}</PanelLink>
        <PanelLink to="/settings/sign-in-methods">{t`Sign-in methods`}</PanelLink>
        <PanelLink to="/settings/sessions">{t`Sessions`}</PanelLink>
        <PanelLink to="/settings/tokens">{t`API tokens`}</PanelLink>
      </PanelGroup>
    </ContextPanel>
  );
}
