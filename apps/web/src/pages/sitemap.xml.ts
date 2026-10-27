// sitemap.xml: home, catalog, categories, every active product, the legal
// pages and the Libro de Reclamaciones, in the three languages with their
// hreflang alternates. Product slugs are the same in every locale.
import type { APIRoute } from 'astro';
import { SITE_URL } from 'astro:env/server';
import { browseProducts, getCategories } from '../lib/catalog';
import { sitemapPaths, sitemapXml } from '../lib/seo';

const PAGE_SIZE = 100;
const MAX_PAGES = 50; // 5 000 products; split into a sitemap index beyond that

export const GET: APIRoute = async ({ locals }) => {
  try {
    const categories = await getCategories(locals.api, 'es');
    const slugs: string[] = [];
    for (let page = 1; page <= MAX_PAGES; page++) {
      const res = await browseProducts(locals.api, { locale: 'es', page, pageSize: PAGE_SIZE });
      slugs.push(...res.items.map((p) => p.slug));
      if (slugs.length >= res.total || res.items.length < PAGE_SIZE) break;
    }
    const xml = sitemapXml(SITE_URL, sitemapPaths(categories.map((c) => c.slug), slugs));
    return new Response(xml, {
      headers: { 'Content-Type': 'application/xml; charset=utf-8', 'Cache-Control': 'public, max-age=3600' },
    });
  } catch {
    // Crawlers retry later; an empty sitemap would tell them the pages are gone.
    return new Response('catalog unavailable', { status: 503, headers: { 'Retry-After': '600' } });
  }
};
