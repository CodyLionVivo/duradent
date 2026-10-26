import type { APIRoute } from 'astro';
import { isLocale, localePath } from '../../../i18n';
import { clearSession } from '../../../lib/session';

export const POST: APIRoute = async ({ params, locals, cookies, redirect }) => {
  const locale = isLocale(params.locale) ? params.locale : 'es';
  // Revoke the session server-side too, not just the cookie.
  await locals.api.POST('/v1/auth/logout').catch(() => undefined);
  clearSession(cookies);
  return redirect(localePath(locale), 303);
};
