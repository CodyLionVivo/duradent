import { describe, expect, it } from 'vitest';
import { safeReturnPath } from './urls';

describe('safeReturnPath', () => {
  it('keeps same-site paths', () => {
    expect(safeReturnPath('/es/catalog?q=niti')).toBe('/es/catalog?q=niti');
  });

  it.each(['//evil.com', '/\\evil.com', 'https://evil.com', 'javascript:alert(1)', '', null])(
    'rejects %s',
    (value) => {
      expect(safeReturnPath(value)).toBe('/');
    },
  );
});
