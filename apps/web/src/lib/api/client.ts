// Typed clients for the Go API. Types come from services/api/openapi.yaml via
// `npm run gen:api`; never edit schema.d.ts by hand.
//
// Server-side only: in Docker the API is http://api:8080, on AWS the internal
// ALB. Browsers never call the API directly in this phase.
import createClient, { type Middleware } from 'openapi-fetch';
import { ADMIN_TOKEN, API_URL } from 'astro:env/server';
import type { components, paths } from './schema';

export type Schemas = components['schemas'];
export type Problem = Schemas['Problem'];

export const api = createClient<paths>({ baseUrl: API_URL });

const adminAuth: Middleware = {
  onRequest({ request }) {
    request.headers.set('Authorization', `Bearer ${ADMIN_TOKEN ?? ''}`);
    return request;
  },
};

/** Client for /v1/admin routes; adds the admin bearer token. */
export const adminApi = createClient<paths>({ baseUrl: API_URL });
adminApi.use(adminAuth);

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
  if (result.data !== undefined && result.response.ok) return result.data;
  throw new ApiError(result.response.status, result.error as Problem | undefined);
}
