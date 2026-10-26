// Uploads product images straight to S3 with presigned URLs obtained through
// the admin proxy, then attaches them to the product and reloads the page.
import { useState } from 'preact/hooks';

interface Props {
  productId: string;
  productName: string;
  labels: { upload: string; uploading: string; failed: string; hint: string };
}

const ACCEPTED = ['image/jpeg', 'image/png', 'image/webp', 'image/avif'];

async function post(productId: string, body: unknown) {
  const res = await fetch(`/admin/api/products/${productId}/images`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
  return data;
}

export default function ImageUploader({ productId, productName, labels }: Props) {
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<string[]>([]);

  async function upload(files: FileList | null) {
    if (!files?.length) return;
    setBusy(true);
    const failed: string[] = [];
    for (const file of Array.from(files)) {
      try {
        if (!ACCEPTED.includes(file.type)) throw new Error(file.type || 'unknown type');
        const up = await post(productId, { action: 'presign', content_type: file.type });
        const put = await fetch(up.upload_url, { method: up.method, headers: up.headers, body: file });
        if (!put.ok) throw new Error(`S3 ${put.status}`);
        await post(productId, { action: 'attach', key: up.key, alt: { es: productName } });
      } catch (err) {
        failed.push(`${labels.failed.replace('{name}', file.name)} ${(err as Error).message}`);
      }
    }
    setBusy(false);
    setErrors(failed);
    if (failed.length === 0) window.location.reload();
  }

  return (
    <div>
      <label class={`btn-secondary cursor-pointer ${busy ? 'pointer-events-none opacity-60' : ''}`}>
        <input
          type="file"
          accept={ACCEPTED.join(',')}
          multiple
          class="sr-only"
          disabled={busy}
          onChange={(e) => upload((e.target as HTMLInputElement).files)}
        />
        {busy ? labels.uploading : labels.upload}
      </label>
      <p class="mt-2 text-xs text-muted">{labels.hint}</p>
      {errors.length > 0 && (
        <ul role="alert" class="mt-2 space-y-1 text-sm text-red-700">
          {errors.map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
