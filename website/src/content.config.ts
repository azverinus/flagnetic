import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

// The pages are the Markdown files of docs/, the same ones GitHub renders.
// A README stands for its directory, so docs/README.md is the home page and
// docs/sdks/README.md is /sdks/.
export const collections = {
  docs: defineCollection({
    loader: glob({
      pattern: '**/*.md',
      base: '../docs',
      generateId: ({ entry }) => entry.replace(/\.md$/, '').replace(/(^|\/)README$/i, '$1index').toLowerCase(),
    }),
    schema: docsSchema(),
  }),
};
