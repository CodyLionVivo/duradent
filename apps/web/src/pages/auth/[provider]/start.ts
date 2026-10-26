// Starts an OIDC sign-in: asks the API for the provider URL, binds the state
// to this browser with a short-lived cookie (login-CSRF protection) and sends
// the browser to Google or Microsoft.
import type { APIRoute } from 'astro';
import { isLocale, localePath } from '../../../i18n';
import { unwrap } from '../../../lib/api/client';
import { OAUTH_STATE_COOKIE } from '../../../lib/session';
import { afterSignIn } from '../../../lib/account';

export const GET: APIRoute = async ({ params, url, locals, cookies, redirect }) => {
  const locale = isLocale(url.searchParams.get('locale') ?? '') ? (url.searchParams.get('locale') as 'es') : 'es';
  const returnTo = afterSignIn(url.searchParams.get('return_to'), localePath(locale, '/account'));
  try {
    const start = unwrap(
      await locals.api.POST('/v1/auth/oidc/{provider}/start', {
        params: { path: { provider: params.provider as 'google' | 'microsoft' } },
        body: { return_to: returnTo },
      }),
    );
    cookies.set(OAUTH_STATE_COOKIE, `${start.state}|${locale}`, {
      path: '/auth',
      httpOnly: true,
      sameSite: 'lax',
      secure: import.meta.env.PROD,
      maxAge: 600,
    });
    return redirect(start.authorization_url, 302);
  } catch {
    return redirect(`${localePath(locale, '/account/login')}?error=identity.provider_unknown`, 302);
  }
};
