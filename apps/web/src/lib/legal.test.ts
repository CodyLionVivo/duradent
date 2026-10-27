import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { LOCALES } from '../i18n/locales';
import { LEGAL_PAGES, isLegalPage } from './legal';

describe('legal pages', () => {
  it.each(LOCALES.flatMap((l) => LEGAL_PAGES.map((p) => [l, p] as const)))('%s/%s exists with a title and the store phone', (locale, page) => {
    const md = readFileSync(new URL(`../content/legal/${locale}/${page}.md`, import.meta.url), 'utf8');
    expect(md).toMatch(/^---\r?\ntitle: .+\r?\ndescription: .+\r?\nupdated: \d{4}-\d{2}-\d{2}\r?\n---/);
    expect(md).toContain('+51 933 741 379');
  });

  it('only accepts known slugs', () => {
    expect(isLegalPage('privacy')).toBe(true);
    expect(isLegalPage('../secret')).toBe(false);
  });
});
