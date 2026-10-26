import type { APIRoute } from 'astro';
import { CURRENCY_COOKIE } from '../../../middleware';
import { isCurrency } from '../../../i18n';
import { safeReturnPath } from '../../../lib/urls';

// Stores the display currency in a cookie and returns to the page. Works as a
// plain HTML form, without JavaScript.
export const POST: APIRoute = async ({ request, cookies, redirect }) => {
  const form = await request.formData();
  const currency = form.get('currency');
  if (typeof currency === 'string' && isCurrency(currency)) {
    cookies.set(CURRENCY_COOKIE, currency, {
      path: '/',
      maxAge: 60 * 60 * 24 * 365,
      sameSite: 'lax',
      httpOnly: true,
      secure: import.meta.env.PROD,
    });
  }
  return redirect(safeReturnPath(form.get('return_to')), 303);
};
