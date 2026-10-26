// robots.txt: production lets crawlers in (except the admin and the sign-in
// callbacks) and points them to the sitemap; staging and local shut them out.
import type { APIRoute } from 'astro';
import { ALLOW_INDEXING, SITE_URL } from 'astro:env/server';
import { robotsTxt } from '../lib/seo';

export const GET: APIRoute = () =>
  new Response(robotsTxt(SITE_URL, ALLOW_INDEXING), {
    headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'public, max-age=3600' },
  });
