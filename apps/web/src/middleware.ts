import { defineMiddleware } from 'astro:middleware';
import { DEFAULT_CURRENCY, DEFAULT_LOCALE, isCurrency, isLocale } from './i18n';
import { createApi } from './lib/api/client';
import { CART_COOKIE, SESSION_COOKIE, clearSession } from './lib/session';
import { proxiedClientIP } from './lib/urls';
import { TRUST_PROXY } from 'astro:env/server';

export const CURRENCY_COOKIE = 'currency';

export const onRequest = defineMiddleware(async (context, next) => {
  const { url, cookies, locals, request } = context;
  const first = url.pathname.split('/')[1];

  locals.locale = isLocale(first) ? first : DEFAULT_LOCALE;
  const currency = cookies.get(CURRENCY_COOKIE)?.value;
  locals.currency = isCurrency(currency) ? currency : DEFAULT_CURRENCY;

  // Every page talks to the API as the visitor: their session decides which
  // price list they see and what they may do; anonymous visitors carry the
  // key of their cart.
  let session = cookies.get(SESSION_COOKIE)?.value;
  const cartKey = cookies.get(CART_COOKIE)?.value;
  let clientIP: string | undefined;
  if (TRUST_PROXY) {
    clientIP = proxiedClientIP(request.headers.get('x-forwarded-for'));
  } else {
    try {
      clientIP = context.clientAddress;
    } catch {
      // Not available during prerendering.
    }
  }
  const userAgent = request.headers.get('user-agent') ?? undefined;
  locals.clientIP = clientIP;
  locals.api = createApi({ session, cartKey, clientIP, userAgent });
  locals.user = null;
  locals.cartCount = 0;
  if (session) {
    const res = await locals.api.GET('/v1/me');
    if (res.response.ok && res.data) {
      locals.user = res.data.account;
      locals.customer = res.data.customer ?? null;
    } else if (res.response.status === 401) {
      clearSession(cookies);
      session = undefined;
      locals.api = createApi({ cartKey, clientIP, userAgent });
    }
  }

  // The admin panel requires an account with the admin role.
  if (first === 'admin') {
    if (!locals.user) {
      return context.redirect(`/${DEFAULT_LOCALE}/account/login?next=${encodeURIComponent(url.pathname + url.search)}`);
    }
    if (locals.user.role !== 'admin') {
      return new Response('Forbidden', { status: 403 });
    }
  } else if ((session || cartKey) && request.method === 'GET' && isLocale(first)) {
    // Item count for the header's cart link.
    const res = await locals.api.GET('/v1/cart', { params: { query: { currency: locals.currency } } });
    if (res.data) locals.cartCount = res.data.item_count;
  }

  const response = await next();
  response.headers.set('X-Content-Type-Options', 'nosniff');
  response.headers.set('Referrer-Policy', 'strict-origin-when-cross-origin');
  response.headers.set('X-Frame-Options', 'DENY');
  if (first === 'admin' || /\/(account|cart|checkout|orders)/.test(url.pathname)) {
    response.headers.set('Cache-Control', 'no-store');
  }
  return response;
});
