// Server-side data access for store pages.
import { unwrap, type ApiClient, type Schemas } from './api/client';
import type { Currency, Locale } from '../i18n';

export type Category = Schemas['Category'];
export type ProductSummary = Schemas['ProductSummary'];
export type ProductDetail = Schemas['ProductDetail'];
export type Quote = Schemas['Quotes']['items'][number];

const TTL_MS = 60_000;
const categoryCache = new Map<Locale, { at: number; data: Category[] }>();

/** Categories change rarely and every page header needs them: cache for a minute. */
export async function getCategories(api: ApiClient, locale: Locale): Promise<Category[]> {
  const hit = categoryCache.get(locale);
  if (hit && Date.now() - hit.at < TTL_MS) return hit.data;
  const data = unwrap(await api.GET('/v1/catalog/categories', { params: { query: { locale } } }));
  categoryCache.set(locale, { at: Date.now(), data });
  return data;
}

export function topLevel(categories: Category[]): Category[] {
  return categories.filter((c) => c.parent_id === null);
}

export function childrenOf(categories: Category[], parentId: string): Category[] {
  return categories.filter((c) => c.parent_id === parentId);
}

export async function browseProducts(api: ApiClient, opts: {
  locale: Locale;
  category?: string;
  q?: string;
  page?: number;
  pageSize?: number;
}) {
  return unwrap(
    await api.GET('/v1/catalog/products', {
      params: {
        query: {
          locale: opts.locale,
          category: opts.category || undefined,
          q: opts.q || undefined,
          page: opts.page,
          page_size: opts.pageSize,
        },
      },
    }),
  );
}

/** Returns the product, or null when it does not exist or is not published. */
export async function getProduct(api: ApiClient, slug: string, locale: Locale): Promise<ProductDetail | null> {
  const res = await api.GET('/v1/catalog/products/{slug}', { params: { path: { slug }, query: { locale } } });
  if (res.response.status === 404) return null;
  return unwrap(res);
}

/**
 * Prices for the visitor keyed by variant id: the signed-in customer's list,
 * or the public list. Variants without a price are absent.
 */
export async function getQuotes(api: ApiClient, variantIds: string[], currency: Currency): Promise<Map<string, Quote>> {
  const ids = [...new Set(variantIds)].filter(Boolean);
  if (ids.length === 0) return new Map();
  const data = unwrap(await api.GET('/v1/pricing/quotes', { params: { query: { currency, variant_id: ids } } }));
  return new Map(data.items.map((q) => [q.variant_id, q]));
}
