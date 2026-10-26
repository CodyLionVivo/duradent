// Same-origin proxy for the image uploader island: the browser never sees the
// admin token. Protected by the /admin cookie gate in middleware.ts.
import type { APIRoute } from 'astro';
import { adminApi, ApiError, unwrap } from '../../../../../lib/api/client';
import { errorMessage } from '../../../../../lib/admin';

type Body =
  | { action: 'presign'; content_type: 'image/jpeg' | 'image/png' | 'image/webp' | 'image/avif' }
  | { action: 'attach'; key: string; alt?: Record<string, string> };

export const POST: APIRoute = async ({ params, request }) => {
  const id = params.id ?? '';
  let body: Body;
  try {
    body = (await request.json()) as Body;
  } catch {
    return Response.json({ error: 'invalid JSON' }, { status: 400 });
  }
  try {
    if (body.action === 'presign') {
      const up = unwrap(
        await adminApi.POST('/v1/admin/catalog/products/{id}/images/uploads', {
          params: { path: { id } },
          body: { content_type: body.content_type },
        }),
      );
      return Response.json(up);
    }
    if (body.action === 'attach') {
      unwrap(
        await adminApi.POST('/v1/admin/catalog/products/{id}/images', {
          params: { path: { id } },
          body: { key: body.key, alt: body.alt },
        }),
      );
      return Response.json({ ok: true }, { status: 201 });
    }
    return Response.json({ error: 'unknown action' }, { status: 400 });
  } catch (err) {
    const status = err instanceof ApiError ? err.status : 500;
    return Response.json({ error: errorMessage(err) }, { status });
  }
};
