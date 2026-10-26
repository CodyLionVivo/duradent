import { describe, expect, it } from 'vitest';
import { proxiedClientIP, safeReturnPath } from './urls';

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

describe('proxiedClientIP', () => {
  it('takes the address the load balancer appended', () => {
    expect(proxiedClientIP('1.2.3.4, 190.40.1.9')).toBe('190.40.1.9');
    expect(proxiedClientIP('190.40.1.9')).toBe('190.40.1.9');
    expect(proxiedClientIP(null)).toBeUndefined();
    expect(proxiedClientIP('')).toBeUndefined();
  });
});
