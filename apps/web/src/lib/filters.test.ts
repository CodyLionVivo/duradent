import { describe, expect, it } from 'vitest';
import { activeCount, clearedUrl, readFilters, subtreeCounts, withoutValue } from './filters';

const url = new URL('http://shop.test/es/catalog/brackets?q=roth&brand=Acme&brand=Acme&brand=Beta&attr=slot:022&attr=bad&attr=arch:upper&sort=newest&page=3');

describe('readFilters', () => {
  it('reads, deduplicates and validates', () => {
    const f = readFilters(url);
    expect(f).toEqual({ q: 'roth', brands: ['Acme', 'Beta'], attrs: ['slot:022', 'arch:upper'], sort: 'newest' });
    expect(activeCount(f)).toBe(4);
    expect(readFilters(new URL('http://shop.test/es/catalog?sort=price')).sort).toBe('name');
  });
});

describe('urls', () => {
  it('removes one value and resets the page', () => {
    expect(withoutValue(url, 'attr', 'slot:022')).toBe('/es/catalog/brackets?q=roth&brand=Acme&brand=Acme&brand=Beta&sort=newest&attr=bad&attr=arch%3Aupper');
    expect(withoutValue(url, 'brand', 'Acme')).not.toContain('Acme');
  });
  it('clears filters but keeps search and sort', () => {
    expect(clearedUrl(url)).toBe('/es/catalog/brackets?q=roth&sort=newest');
  });
});

describe('subtreeCounts', () => {
  it('adds subcategories to their parent', () => {
    const cats = [
      { id: '1', slug: 'brackets', parent_id: null },
      { id: '2', slug: 'metalicos', parent_id: '1' },
      { id: '3', slug: 'esteticos', parent_id: '1' },
      { id: '4', slug: 'arcos', parent_id: null },
    ];
    const n = subtreeCounts(cats, [{ slug: 'metalicos', count: 3 }, { slug: 'esteticos', count: 2 }, { slug: 'brackets', count: 1 }]);
    expect(n.get('brackets')).toBe(6);
    expect(n.get('metalicos')).toBe(3);
    expect(n.get('arcos')).toBe(0);
  });
});
