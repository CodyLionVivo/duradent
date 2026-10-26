// Session and cart cookies. The API issues opaque tokens; the web keeps them
// in httpOnly cookies so page scripts can never read them.
import type { AstroCookies } from 'astro';
import { createApi } from './api/client';

export const SESSION_COOKIE = 'dd_session';
export const OAUTH_STATE_COOKIE = 'dd_oauth_state';
/** Key of an anonymous visitor's cart; merged into the account at sign-in. */
export const CART_COOKIE = 'dd_cart';

export function setSession(cookies: AstroCookies, token: string, expiresAt: string): void {
  cookies.set(SESSION_COOKIE, token, {
    path: '/',
    httpOnly: true,
    sameSite: 'lax', // sent on the top-level redirect back from Google/Microsoft
    secure: import.meta.env.PROD,
    expires: new Date(expiresAt),
  });
}

export function clearSession(cookies: AstroCookies): void {
  cookies.delete(SESSION_COOKIE, { path: '/' });
}

export function setCartKey(cookies: AstroCookies, key: string): void {
  cookies.set(CART_COOKIE, key, {
    path: '/',
    httpOnly: true,
    sameSite: 'lax',
    secure: import.meta.env.PROD,
    maxAge: 60 * 60 * 24 * 30,
  });
}

/** After sign-in: move the anonymous cart into the account and forget its key. */
export async function adoptCart(cookies: AstroCookies, session: string, clientIP?: string): Promise<void> {
  const key = cookies.get(CART_COOKIE)?.value;
  if (!key) return;
  await createApi({ session, clientIP })
    .POST('/v1/cart/merge', { body: { key } })
    .catch(() => undefined);
  cookies.delete(CART_COOKIE, { path: '/' });
}
