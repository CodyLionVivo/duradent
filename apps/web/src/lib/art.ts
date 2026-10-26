// Picks the illustration shown for a product without photos: the top-level
// category it belongs to (brackets, arcos…), and a tint that varies per
// product so a grid of placeholders does not look repetitive.

export const ART_KINDS = ['brackets', 'tubos-bandas', 'arcos', 'elasticos', 'instrumental', 'adhesivos'] as const;
export type ArtKind = (typeof ART_KINDS)[number] | 'tooth';

interface CategoryRef {
  id: string;
  slug: string;
  parent_id: string | null;
}

/** Illustration for a category: its root's slug, or a tooth when unknown. */
export function artKind(categories: CategoryRef[], categoryId: string | null | undefined): ArtKind {
  const byId = new Map(categories.map((c) => [c.id, c]));
  let current = categoryId ? byId.get(categoryId) : undefined;
  for (let depth = 0; current?.parent_id && depth < 5; depth++) {
    current = byId.get(current.parent_id) ?? current;
    if (!current.parent_id) break;
  }
  const slug = current?.slug;
  return (ART_KINDS as readonly string[]).includes(slug ?? '') ? (slug as ArtKind) : 'tooth';
}

export const ART_TINTS = 3;

/** Stable tint index (0..ART_TINTS-1) for a product slug. */
export function artTint(seed: string): number {
  let h = 0;
  for (const ch of seed) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return h % ART_TINTS;
}
