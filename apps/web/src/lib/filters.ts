// Catalog filters live in the URL (a plain GET form), so filtered pages can be
// shared, bookmarked and work without JavaScript:
//   ?q=roth&brand=Duradent&attr=slot:022&attr=slot:018&sort=newest&page=2
import type { CatalogSort } from './catalog';

export interface CatalogFilters {
  q: string;
  brands: string[];
  /** "code:value" pairs, e.g. "slot:022". */
  attrs: string[];
  sort: CatalogSort;
}

const unique = (values: string[], max = 20) =>
  [...new Set(values.map((v) => v.trim()).filter((v) => v && v.length <= 80))].slice(0, max);

export function readFilters(url: URL): CatalogFilters {
  const p = url.searchParams;
  return {
    q: (p.get('q') ?? '').trim().slice(0, 100),
    brands: unique(p.getAll('brand')),
    attrs: unique(p.getAll('attr')).filter((a) => /^[a-z0-9_]+:.+$/.test(a)),
    sort: p.get('sort') === 'newest' ? 'newest' : 'name',
  };
}

/** Number of brand and attribute values selected (for the mobile button). */
export function activeCount(f: CatalogFilters): number {
  return f.brands.length + f.attrs.length;
}

/** The current page's URL without one filter value (and back to page 1). */
export function withoutValue(url: URL, name: 'brand' | 'attr', value: string): string {
  const next = new URL(url);
  const keep = next.searchParams.getAll(name).filter((v) => v !== value);
  next.searchParams.delete(name);
  for (const v of keep) next.searchParams.append(name, v);
  next.searchParams.delete('page');
  return next.pathname + next.search;
}

/** The current path keeping only the search text and the sort order. */
export function clearedUrl(url: URL): string {
  const next = new URL(url.pathname, url);
  const q = url.searchParams.get('q');
  const sort = url.searchParams.get('sort');
  if (q) next.searchParams.set('q', q);
  if (sort) next.searchParams.set('sort', sort);
  return next.pathname + next.search;
}

interface CategoryNode {
  slug: string;
  id: string;
  parent_id: string | null;
}

/** Products per category including its subcategories (facet counts are per category). */
export function subtreeCounts(categories: CategoryNode[], direct: { slug: string; count: number }[]): Map<string, number> {
  const bySlug = new Map(direct.map((d) => [d.slug, d.count]));
  const children = new Map<string, CategoryNode[]>();
  for (const c of categories) {
    if (c.parent_id) children.set(c.parent_id, [...(children.get(c.parent_id) ?? []), c]);
  }
  const out = new Map<string, number>();
  const visit = (c: CategoryNode, depth = 0): number => {
    if (out.has(c.slug)) return out.get(c.slug)!;
    let n = bySlug.get(c.slug) ?? 0;
    if (depth < 10) for (const child of children.get(c.id) ?? []) n += visit(child, depth + 1);
    out.set(c.slug, n);
    return n;
  };
  categories.forEach((c) => visit(c));
  return out;
}
