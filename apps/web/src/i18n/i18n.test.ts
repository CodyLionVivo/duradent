import { describe, expect, it } from 'vitest';
import es from './messages/es.json';
import en from './messages/en.json';
import ptBR from './messages/pt-br.json';
import { formatMoney, localePath, negotiateLocale, switchLocalePath, useTranslations } from './index';

function keys(obj: object, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) =>
    typeof v === 'string' ? [`${prefix}${k}`] : keys(v as object, `${prefix}${k}.`),
  );
}

describe('translation files', () => {
  const store = keys(es).filter((k) => !k.startsWith('admin.'));

  it.each([
    ['en', en],
    ['pt-br', ptBR],
  ])('%s translates every store string and nothing else', (_, messages) => {
    const have = keys(messages);
    expect(store.filter((k) => !have.includes(k))).toEqual([]);
    expect(have.filter((k) => !store.includes(k))).toEqual([]);
  });

  it('keeps the same {placeholders} in every language', () => {
    const es_t = useTranslations('es');
    for (const locale of ['en', 'pt-br'] as const) {
      const t = useTranslations(locale);
      for (const k of store) {
        const placeholders = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort().join();
        // @ts-expect-error -- iterating dynamic keys
        expect(placeholders(t(k)), `${locale}:${k}`).toBe(placeholders(es_t(k)));
      }
    }
  });
});

describe('useTranslations', () => {
  it('interpolates parameters', () => {
    expect(useTranslations('es')('catalog.resultCount', { count: 3 })).toBe('3 productos');
    expect(useTranslations('en')('pagination.page', { page: 2, pages: 5 })).toBe('Page 2 of 5');
  });

  it('falls back to Spanish for keys a locale does not define (admin)', () => {
    expect(useTranslations('en')('admin.products.title')).toBe('Productos');
  });
});

describe('formatMoney', () => {
  it('formats soles with Peruvian conventions', () => {
    expect(formatMoney('1234.5', 'es')).toMatch(/S\/\s?1,234\.50/);
  });

  it('formats soles with each locale’s separators', () => {
    expect(formatMoney('1234.5', 'en')).toMatch(/PEN\s?1,234\.50/);
    expect(formatMoney('1234.5', 'pt-br')).toMatch(/PEN\s?1\.234,50/);
  });
});

describe('locale routing helpers', () => {
  it('builds locale paths', () => {
    expect(localePath('es')).toBe('/es');
    expect(localePath('pt-br', '/catalog/brackets')).toBe('/pt-br/catalog/brackets');
  });

  it('switches locale keeping the rest of the URL', () => {
    const url = new URL('http://x/es/product/kit-roth?variant=abc');
    expect(switchLocalePath(url, 'en')).toBe('/en/product/kit-roth?variant=abc');
    expect(switchLocalePath(new URL('http://x/es'), 'pt-br')).toBe('/pt-br');
  });

  it('negotiates the locale from Accept-Language', () => {
    expect(negotiateLocale('pt-BR,pt;q=0.9,en;q=0.8')).toBe('pt-br');
    expect(negotiateLocale('fr-FR,en;q=0.5')).toBe('en');
    expect(negotiateLocale('de-DE')).toBe('es');
    expect(negotiateLocale(null)).toBe('es');
  });
});
