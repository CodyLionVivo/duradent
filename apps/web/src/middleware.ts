import { defineMiddleware } from 'astro:middleware';
import { DEFAULT_CURRENCY, DEFAULT_LOCALE, isCurrency, isLocale } from './i18n';
import { createApi } from './lib/api/client';
import { SESSION_COOKIE, clearSession } from './lib/session';

export const CURRENCY_COOKIE = 'currency';

export const onRequest = defineMiddleware(async (context, next) => {
  const { url, cookies, locals, request } = context;
  const first = url.pathname.split('/')[1];

  locals.locale = isLocale(first) ? first : DEFAULT_LOCALE;
  const currency = cookies.get(CURRENCY_COOKIE)?.value;
  locals.currency = isCurrency(currency) ? currency : DEFAULT_CURRENCY;

  // Every page talks to the API as the visitor: their session decides which
  // price list they see and what they may do.
  const session = cookies.get(SESSION_COOKIE)?.value;
  let clientIP: string | undefined;
  try {
    clientIP = context.clientAddress;
  } catch {
    // Not available during prerendering.
  }
  locals.api = createApi({ session, clientIP, userAgent: request.headers.get('user-agent') ?? undefined });
  locals.user = null;
  if (session) {
    const res = await locals.api.GET('/v1/me');
    if (res.response.ok && res.data) {
      locals.user = res.data.account;
      locals.customer = res.data.customer ?? null;
    } else if (res.response.status === 401) {
      clearSession(cookies);
      locals.api = createApi({ clientIP });
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
  }

  const response = await next();
  response.headers.set('X-Content-Type-Options', 'nosniff');
  response.headers.set('Referrer-Policy', 'strict-origin-when-cross-origin');
  response.headers.set('X-Frame-Options', 'DENY');
  if (first === 'admin' || url.pathname.includes('/account')) response.headers.set('Cache-Control', 'no-store');
  return response;
});
