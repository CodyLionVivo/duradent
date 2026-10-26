import type { APIRoute } from 'astro';
import { ADMIN_COOKIE } from '../../middleware';

export const POST: APIRoute = ({ cookies, redirect }) => {
  cookies.delete(ADMIN_COOKIE, { path: '/admin' });
  return redirect('/admin/login', 303);
};
