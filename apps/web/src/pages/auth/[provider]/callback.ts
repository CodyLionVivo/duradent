// Where Google/Microsoft send the browser back. The state in the URL must match
// the cookie set when the flow started in this same browser; the API then
// redeems the code (once) and issues our session.
import type { APIRoute } from 'astro';
import { timingSafeEqual } from 'node:crypto';
import { isLocale, localePath, type Locale } from '../../../i18n';
import { ApiError, unwrap } from '../../../lib/api/client';
import { OAUTH_STATE_COOKIE, adoptCart, setSession } from '../../../lib/session';
import { safeReturnPath } from '../../../lib/urls';

function same(a: string, b: string): boolean {
  const x = Buffer.from(a);
  const y = Buffer.from(b);
  return x.length === y.length && timingSafeEqual(x, y);
}

export const GET: APIRoute = async ({ params, url, locals, cookies, redirect }) => {
  const [expected = '', cookieLocale = 'es'] = (cookies.get(OAUTH_STATE_COOKIE)?.value ?? '').split('|');
  cookies.delete(OAUTH_STATE_COOKIE, { path: '/auth' });
  const locale: Locale = isLocale(cookieLocale) ? cookieLocale : 'es';
  const fail = (code: string) => redirect(`${localePath(locale, '/account/login')}?error=${encodeURIComponent(code)}`, 302);

  if (url.searchParams.get('error')) return fail('identity.provider_rejected'); // user cancelled or provider error
  const state = url.searchParams.get('state') ?? '';
  const code = url.searchParams.get('code') ?? '';
  if (!expected || !state || !code || !same(state, expected)) return fail('identity.login_state_invalid');

  try {
    const signed = unwrap(
      await locals.api.POST('/v1/auth/oidc/{provider}/callback', {
        params: { path: { provider: params.provider as 'google' | 'microsoft' } },
        body: { code, state },
      }),
    );
    setSession(cookies, signed.token, signed.expires_at);
    await adoptCart(cookies, signed.token, locals.clientIP);
    const target = safeReturnPath(signed.return_to ?? null);
    return redirect(target === '/' ? localePath(locale, '/account') : target, 302);
  } catch (err) {
    return fail(err instanceof ApiError && err.problem?.code ? err.problem.code : 'identity.provider_rejected');
  }
};
