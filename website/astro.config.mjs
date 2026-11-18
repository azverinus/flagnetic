import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightOpenAPI, { openAPISidebarGroups } from 'starlight-openapi';
import docsPages from './plugins/docs-pages.mjs';

// The site lives at https://azverinus.github.io/flagnetic/ on GitHub Pages.
const base = '/flagnetic';

export default defineConfig({
  site: 'https://azverinus.github.io',
  base,
  markdown: {
    remarkPlugins: [docsPages({ base })],
  },
  integrations: [
    starlight({
      title: 'Flagnetic',
      description: 'Self-hosted, open-source feature flags and experiments.',
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/azverinus/flagnetic' }],
      editLink: { baseUrl: 'https://github.com/azverinus/flagnetic/edit/main/' },
      plugins: [
        starlightOpenAPI([
          { base: 'api/management', label: 'Management API', schema: '../api/management.yaml' },
          { base: 'api/sdk', label: 'SDK API', schema: '../api/sdk.yaml' },
          { base: 'api/scim', label: 'SCIM API', schema: '../api/scim.yaml' },
        ]),
      ],
      sidebar: [
        {
          label: 'Start',
          items: ['quickstart', 'install'],
        },
        {
          label: 'Run',
          items: ['kubernetes', 'upgrades', 'operations', 'mail'],
        },
        {
          label: 'Use',
          items: [
            'access',
            'flags',
            'targeting',
            'segments',
            'experiments',
            'sample-ratio-check',
            'governance',
            'change-requests',
            'transfer',
          ],
        },
        {
          label: 'SDKs',
          items: [
            { label: 'Choosing an SDK', link: '/sdks/' },
            'sdks/javascript',
            'sdks/python',
            'sdks/ruby',
            'sdks/dart',
            'sdks/keys',
            'sdks/ruleset',
            'sdks/evaluate',
            'sdks/events',
            'sdks/stream',
            'sdks/compatibility',
            'sdks/behaviour-matrix',
            'sdks/conformance',
          ],
        },
        {
          label: 'Integrate',
          items: ['sso', 'scim', 'webhooks', 'sinks', 'exported-events'],
        },
        {
          label: 'Reference',
          items: ['glossary', 'configuration', 'compatibility', 'verify', ...openAPISidebarGroups],
        },
      ],
    }),
  ],
});
