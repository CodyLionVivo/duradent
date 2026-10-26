// Search engines: canonical URLs, language alternates, robots.txt, the
// sitemap and schema.org structured data (JSON-LD). Pure functions: pages
// pass the configured SITE_URL in.
import { HTML_LANG, LOCALES, localePath, type Locale } from '../i18n';
import { LEGAL_PAGES } from './legal';

/** Absolute URL of a path on the public site. */
export function absolute(site: string, path: string): string {
  return new URL(path, site).href;
}

/**
 * The URL search engines should index for a page: its path, plus ?page=N on
 * later catalog pages. Filters, sorting, searches and ?variant= are views of
 * the same page.
 */
export function canonicalPath(url: URL): string {
  const page = Number.parseInt(url.searchParams.get('page') ?? '', 10);
  return page > 1 ? `${url.pathname}?page=${page}` : url.pathname;
}

/** The same path in every locale (the first segment is the locale). */
export function localeVariants(path: string): { hreflang: string; locale: Locale; path: string }[] {
  const [, , ...rest] = path.split('/');
  const tail = rest.length ? `/${rest.join('/')}` : '';
  return LOCALES.map((l) => ({ hreflang: HTML_LANG[l], locale: l, path: `/${l}${tail}` }));
}

/** <link rel="alternate" hreflang> entries, with x-default on Spanish. */
export function alternates(site: string, path: string): { hreflang: string; href: string }[] {
  const all = localeVariants(path).map((v) => ({ hreflang: v.hreflang, href: absolute(site, v.path) }));
  return [...all, { hreflang: 'x-default', href: all[0].href }];
}

/** Pages with someone's data: never in search results. */
export function isPrivatePath(pathname: string): boolean {
  return /^\/[^/]+\/(account|cart|checkout|orders)(\/|$)/.test(pathname);
}

export function robotsTxt(site: string, allowIndexing: boolean): string {
  if (!allowIndexing) return 'User-agent: *\nDisallow: /\n';
  return ['User-agent: *', 'Disallow: /admin', 'Disallow: /auth/', 'Allow: /', '', `Sitemap: ${absolute(site, '/sitemap.xml')}`, ''].join('\n');
}

const escapeXml = (s: string) =>
  s.replace(/[<>&'"]/g, (c) => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', "'": '&apos;', '"': '&quot;' })[c]!);

/** Public pages of the store, without the locale prefix. */
export function sitemapPaths(categorySlugs: string[], productSlugs: string[]): string[] {
  return [
    '/',
    '/catalog',
    ...categorySlugs.map((s) => `/catalog/${s}`),
    ...productSlugs.map((s) => `/product/${s}`),
    ...LEGAL_PAGES.map((p) => `/legal/${p}`),
    '/complaints-book',
  ];
}

/** sitemap.xml: every page in every locale, each listing its alternates. */
export function sitemapXml(site: string, paths: string[]): string {
  const urls = paths.flatMap((p) => {
    const variants = LOCALES.map((l) => localePath(l, p));
    const links = alternates(site, variants[0])
      .map((a) => `    <xhtml:link rel="alternate" hreflang="${a.hreflang}" href="${escapeXml(a.href)}"/>`)
      .join('\n');
    return variants.map((v) => `  <url>\n    <loc>${escapeXml(absolute(site, v))}</loc>\n${links}\n  </url>`);
  });
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">',
    ...urls,
    '</urlset>',
    '',
  ].join('\n');
}

// --- structured data ---------------------------------------------------------------

type JsonLd = Record<string, unknown>;

/** Serializes JSON-LD for a <script> tag; "<" is escaped so data can never close it. */
export function jsonLdScript(data: JsonLd): string {
  return JSON.stringify(data).replace(/</g, '\\u003c');
}

export function breadcrumbJsonLd(site: string, items: { name: string; path: string }[]): JsonLd {
  return {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: items.map((it, i) => ({ '@type': 'ListItem', position: i + 1, name: it.name, item: absolute(site, it.path) })),
  };
}

export interface ProductForSeo {
  name: string;
  description: string;
  brand: string;
  images: { url: string }[];
  variants: { id: string; sku: string; is_default: boolean }[];
}

/**
 * Product with one Offer per priced variant, in soles. Prices are the ones
 * the page shows, without IGV, and say so (valueAddedTaxIncluded: false).
 * Availability is only stated when the stock is known.
 */
export function productJsonLd(
  p: ProductForSeo,
  pageURL: string,
  prices: Map<string, string>,
  stock: Record<string, number>,
): JsonLd {
  const offers = p.variants
    .filter((v) => prices.has(v.id))
    .map((v) => {
      const price = prices.get(v.id)!;
      const known = v.id in stock;
      return {
        '@type': 'Offer',
        sku: v.sku,
        url: `${pageURL}?variant=${v.id}`,
        price,
        priceCurrency: 'PEN',
        priceSpecification: { '@type': 'UnitPriceSpecification', price, priceCurrency: 'PEN', valueAddedTaxIncluded: false },
        itemCondition: 'https://schema.org/NewCondition',
        ...(known ? { availability: stock[v.id] > 0 ? 'https://schema.org/InStock' : 'https://schema.org/OutOfStock' } : {}),
      };
    });
  const main = p.variants.find((v) => v.is_default) ?? p.variants[0];
  return {
    '@context': 'https://schema.org',
    '@type': 'Product',
    name: p.name,
    url: pageURL,
    ...(p.description ? { description: p.description } : {}),
    ...(main ? { sku: main.sku } : {}),
    ...(p.brand ? { brand: { '@type': 'Brand', name: p.brand } } : {}),
    ...(p.images.length ? { image: p.images.map((i) => i.url) } : {}),
    ...(offers.length ? { offers } : {}),
  };
}

export interface StoreForSeo {
  name: string;
  legalName: string;
  description: string;
  taxID: string;
  telephone: string;
  street: string;
  sameAs: string[];
}

/** The physical store and its company, for the home page. */
export function storeJsonLd(site: string, s: StoreForSeo): JsonLd {
  return {
    '@context': 'https://schema.org',
    '@type': 'Store',
    '@id': absolute(site, '/#store'),
    name: s.name,
    legalName: s.legalName,
    description: s.description,
    url: absolute(site, '/'),
    logo: absolute(site, '/brand/logo-1024.png'),
    image: absolute(site, '/brand/og-image.png'),
    telephone: s.telephone,
    taxID: s.taxID,
    address: { '@type': 'PostalAddress', streetAddress: s.street, addressLocality: 'Lima', addressRegion: 'Lima', addressCountry: 'PE' },
    sameAs: s.sameAs,
  };
}
