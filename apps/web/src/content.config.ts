// Content collections. Legal pages are Markdown per language
// (src/content/legal/<locale>/<terms|privacy|returns>.md); Spanish is the
// binding version and the others are translations.
import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { z } from 'astro/zod';

const legal = defineCollection({
  loader: glob({ base: './src/content/legal', pattern: '**/*.md' }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    updated: z.coerce.date(),
  }),
});

export const collections = { legal };
