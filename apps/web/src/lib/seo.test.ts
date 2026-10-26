import { describe, expect, it } from 'vitest';
import {
  alternates,
  canonicalPath,
  isPrivatePath,
  jsonLdScript,
  productJsonLd,
  robotsTxt,
  sitemapPaths,
  sitemapXml,
} from './seo';

const site = 'https://duradent.pe';

describe('canonicalPath', () => {
  it('drops filters, sorting, searches and variants but keeps later pages', () => {
    expect(canonicalPath(new URL('http://x/es/catalog/brackets?brand=3M&sort=newest&page=1'))).toBe('/es/catalog/brackets');
    expect(canonicalPath(new URL('http://x/es/catalog?page=3&attr=slot:022'))).toBe('/es/catalog?page=3');
    expect(canonicalPath(new URL('http://x/en/product/roth?variant=abc'))).toBe('/en/product/roth');
  });
});

describe('alternates', () => {
  it('lists every locale plus x-default on Spanish', () => {
    expect(alternates(site, '/en/legal/terms')).toEqual([
      { hreflang: 'es', href: 'https://duradent.pe/es/legal/terms' },
      { hreflang: 'en', href: 'https://duradent.pe/en/legal/terms' },
      { hreflang: 'pt-BR', href: 'https://duradent.pe/pt-br/legal/terms' },
      { hreflang: 'x-default', href: 'https://duradent.pe/es/legal/terms' },
    ]);
  });
  it('keeps the page of a paginated catalog', () => {
    expect(alternates(site, '/es/catalog/brackets?page=2')[1].href).toBe('https://duradent.pe/en/catalog/brackets?page=2');
  });
});

describe('isPrivatePath', () => {
  it('flags pages with personal data only', () => {
    for (const p of ['/es/account', '/es/account/login', '/en/cart', '/es/checkout', '/pt-br/orders/DD-000001']) expect(isPrivatePath(p)).toBe(true);
    for (const p of ['/es', '/es/catalog', '/es/product/accounting-kit', '/es/complaints-book']) expect(isPrivatePath(p)).toBe(false);
  });
});

describe('robotsTxt', () => {
  it('blocks everything outside production', () => {
    expect(robotsTxt(site, false)).toBe('User-agent: *\nDisallow: /\n');
  });
  it('points production crawlers to the sitemap and keeps them out of the admin', () => {
    const txt = robotsTxt(site, true);
    expect(txt).toContain('Disallow: /admin');
    expect(txt).toContain('Sitemap: https://duradent.pe/sitemap.xml');
  });
});

describe('sitemapXml', () => {
  it('lists each page in each locale with its alternates', () => {
    const xml = sitemapXml(site, sitemapPaths(['brackets'], ['kit-roth&co']));
    expect(xml).toContain('<loc>https://duradent.pe/es</loc>');
    expect(xml).toContain('<loc>https://duradent.pe/pt-br/catalog/brackets</loc>');
    expect(xml).toContain('<loc>https://duradent.pe/en/product/kit-roth&amp;co</loc>');
    expect(xml).toContain('<loc>https://duradent.pe/es/complaints-book</loc>');
    expect(xml).toContain('<xhtml:link rel="alternate" hreflang="x-default" href="https://duradent.pe/es/legal/privacy"/>');
    // home, catalog, 1 category, 1 product, 3 legal pages, complaints book = 8 pages × 3 locales
    expect(xml.match(/<url>/g)).toHaveLength(24);
  });
});

describe('productJsonLd', () => {
  const product = {
    name: 'Brackets Roth',
    description: 'Kit de 20',
    brand: '3M',
    images: [{ url: 'https://cdn/x.jpg' }],
    variants: [
      { id: 'v1', sku: 'BR-018', is_default: false },
      { id: 'v2', sku: 'BR-022', is_default: true },
      { id: 'v3', sku: 'BR-SIN-PRECIO', is_default: false },
    ],
  };
  it('offers each priced variant in soles without IGV', () => {
    const ld = productJsonLd(product, `${site}/es/product/roth`, new Map([['v1', '85.00'], ['v2', '90.00']]), { v1: 0, v2: 12 });
    const offers = ld.offers as Record<string, unknown>[];
    expect(ld.sku).toBe('BR-022');
    expect(ld.brand).toEqual({ '@type': 'Brand', name: '3M' });
    expect(offers).toHaveLength(2);
    expect(offers[0]).toMatchObject({ sku: 'BR-018', price: '85.00', priceCurrency: 'PEN', availability: 'https://schema.org/OutOfStock' });
    expect(offers[1]).toMatchObject({ url: `${site}/es/product/roth?variant=v2`, availability: 'https://schema.org/InStock' });
    expect(offers[0].priceSpecification).toMatchObject({ valueAddedTaxIncluded: false });
  });
  it('says nothing about stock it does not know', () => {
    const ld = productJsonLd(product, `${site}/es/product/roth`, new Map([['v1', '85.00']]), {});
    expect((ld.offers as Record<string, unknown>[])[0]).not.toHaveProperty('availability');
  });
});

describe('jsonLdScript', () => {
  it('cannot close the script tag', () => {
    expect(jsonLdScript({ name: '</script><script>alert(1)</script>' })).not.toContain('</script>');
  });
});
