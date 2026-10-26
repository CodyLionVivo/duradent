import { defineMiddleware } from 'astro:middleware';
import { ADMIN_TOKEN } from 'astro:env/server';
import { timingSafeEqual } from 'node:crypto';
import { DEFAULT_CURRENCY, DEFAULT_LOCALE, isCurrency, isLocale } from './i18n';

export const CURRENCY_COOKIE = 'currency';
export const ADMIN_COOKIE = 'dd_admin';

function sameToken(a: string, b: string): boolean {
  const x = Buffer.from(a);
  const y = Buffer.from(b);
  return x.length === y.length && timingSafeEqual(x, y);
}

/** True when the request carries the admin cookie matching ADMIN_TOKEN. */
export function isAdmin(cookie: string | undefined): boolean {
  return !!ADMIN_TOKEN && !!cookie && sameToken(cookie, ADMIN_TOKEN);
}

export const onRequest = defineMiddleware(async (context, next) => {
  const { url, cookies, locals } = context;
  const first = url.pathname.split('/')[1];

  locals.locale = isLocale(first) ? first : DEFAULT_LOCALE;
  const currency = cookies.get(CURRENCY_COOKIE)?.value;
  locals.currency = isCurrency(currency) ? currency : DEFAULT_CURRENCY;

  // Temporary admin gate until phase 3 brings accounts and roles: the panel
  // asks for ADMIN_TOKEN once and keeps it in an httpOnly cookie.
  if (first === 'admin' && url.pathname !== '/admin/login') {
    if (!isAdmin(cookies.get(ADMIN_COOKIE)?.value)) {
      return context.redirect(`/admin/login?next=${encodeURIComponent(url.pathname + url.search)}`);
    }
  }

  const response = await next();
  response.headers.set('X-Content-Type-Options', 'nosniff');
  response.headers.set('Referrer-Policy', 'strict-origin-when-cross-origin');
  response.headers.set('X-Frame-Options', 'DENY');
  if (first === 'admin') response.headers.set('Cache-Control', 'no-store');
  return response;
});
