import { describe, expect, it } from 'vitest';
import { artKind, artTint } from './art';

const categories = [
  { id: 'b', slug: 'brackets', parent_id: null },
  { id: 'bm', slug: 'brackets-metalicos', parent_id: 'b' },
  { id: 'a', slug: 'arcos', parent_id: null },
  { id: 'x', slug: 'ofertas', parent_id: null },
];

describe('artKind', () => {
  it('uses the top-level category', () => {
    expect(artKind(categories, 'bm')).toBe('brackets');
    expect(artKind(categories, 'a')).toBe('arcos');
  });
  it('falls back to a tooth', () => {
    expect(artKind(categories, 'x')).toBe('tooth');
    expect(artKind(categories, null)).toBe('tooth');
    expect(artKind(categories, 'missing')).toBe('tooth');
  });
});

describe('artTint', () => {
  it('is stable and in range', () => {
    expect(artTint('arco-niti-014')).toBe(artTint('arco-niti-014'));
    for (const s of ['a', 'bracket-roth', 'alicate-distal']) expect(artTint(s)).toBeGreaterThanOrEqual(0);
    expect(new Set(['a', 'b', 'c', 'd', 'e', 'f'].map(artTint)).size).toBeGreaterThan(1);
  });
});
