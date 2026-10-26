// Typed client for the Go API. Types come from services/api/openapi.yaml via
// `npm run gen:api`; never edit schema.d.ts by hand.
import createClient from 'openapi-fetch';
import type { paths } from './schema';

// Server-side only: in Docker the API is reached at http://api:8080, on AWS
// through the internal ALB. Browsers call the API through Astro endpoints.
const baseUrl = import.meta.env.API_URL ?? process.env.API_URL ?? 'http://localhost:8080';

export const api = createClient<paths>({ baseUrl });
