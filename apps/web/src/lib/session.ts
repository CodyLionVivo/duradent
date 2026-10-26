// Session cookie handling. The API issues an opaque token at sign-in; the web
// keeps it in an httpOnly cookie, so page scripts can never read it.
import type { AstroCookies } from 'astro';

export const SESSION_COOKIE = 'dd_session';
export const OAUTH_STATE_COOKIE = 'dd_oauth_state';

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
