// Typed clients for the Go API. Types come from services/api/openapi.yaml via
// `npm run gen:api`; never edit schema.d.ts by hand.
//
// Server-side only: in Docker the API is http://api:8080, on AWS an internal
// endpoint. Browsers never call the API directly: every page gets a client
// bound to the visitor's session (Astro.locals.api, see middleware.ts).
import createClient, { type Client } from 'openapi-fetch';
import { API_URL } from 'astro:env/server';
import type { components, paths } from './schema';

export type Schemas = components['schemas'];
export type Problem = Schemas['Problem'];
export type ApiClient = Client<paths>;

/**
 * A client acting for one visitor: forwards their session token (or their
 * anonymous cart key) and their IP (the API rate-limits sign-in per IP).
 */
export function createApi(
  opts: { session?: string; cartKey?: string; clientIP?: string; userAgent?: string } = {},
): ApiClient {
  const client = createClient<paths>({ baseUrl: API_URL });
  client.use({
    onRequest({ request }) {
      if (opts.session) request.headers.set('Authorization', `Session ${opts.session}`);
      else if (opts.cartKey) request.headers.set('X-Cart-Key', opts.cartKey);
      if (opts.clientIP) request.headers.set('X-Forwarded-For', opts.clientIP);
      if (opts.userAgent) request.headers.set('User-Agent', opts.userAgent);
      return request;
    },
  });
  return client;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly problem?: Problem,
  ) {
    super(problem?.detail ?? problem?.title ?? `API error ${status}`);
  }
}

/** Unwraps an openapi-fetch result, throwing ApiError on non-2xx responses. */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.response.ok) return result.data as T;
  throw new ApiError(result.response.status, result.error as Problem | undefined);
}
