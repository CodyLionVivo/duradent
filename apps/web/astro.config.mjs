// @ts-check
import { defineConfig, envField } from 'astro/config';
import node from '@astrojs/node';
import tailwindcss from '@tailwindcss/vite';
import preact from '@astrojs/preact';

// https://astro.build/config
export default defineConfig({
  output: 'server',
  adapter: node({ mode: 'standalone' }),
  server: { host: true },

  // Locale routing (/es, /en, /pt-br) is handled by src/middleware.ts and the
  // [locale] page folder, so /admin and /api stay outside the locale prefix.

  env: {
    schema: {
      // Read at runtime on the server, never inlined into the bundle.
      API_URL: envField.string({ context: 'server', access: 'secret', default: 'http://localhost:8080' }),
      // Behind the AWS load balancer: take the visitor's IP from X-Forwarded-For.
      TRUST_PROXY: envField.boolean({ context: 'server', access: 'secret', default: false }),
      // Public address of the store, for canonical links, the sitemap and
      // structured data (behind the load balancer the request URL is http).
      SITE_URL: envField.string({ context: 'server', access: 'public', default: 'http://localhost:4321' }),
      // Only production may be indexed: elsewhere robots.txt and every page say noindex.
      ALLOW_INDEXING: envField.boolean({ context: 'server', access: 'public', default: false }),
    },
  },

  vite: {
    plugins: [tailwindcss()],
    // Docker Desktop bind mounts on Windows deliver no file events: poll instead.
    server: { watch: { usePolling: process.env.VITE_POLLING === 'true', interval: 300 } },
  },

  integrations: [preact()],
});
