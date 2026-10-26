// Helpers shared by the admin pages: form parsing and API error messages.
import { ApiError, type Schemas } from './api/client';
import { LOCALES, type Locale } from '../i18n';
import es from '../i18n/messages/es.json';

const errorTexts: Record<string, string> = es.admin.errors;

export type Texts = Schemas['Texts'];

/** Reads name_<locale> / description_<locale> fields into the API's texts map. */
export function textsFromForm(form: FormData): Texts {
  const texts: Texts = {};
  for (const l of LOCALES) {
    const name = String(form.get(`name_${l}`) ?? '').trim();
    const description = String(form.get(`description_${l}`) ?? '').trim();
    if (name || description) texts[l] = { name, description };
  }
  return texts;
}

/** Reads label_<locale> fields into a localized string map. */
export function localizedFromForm(form: FormData, prefix: string): Partial<Record<Locale, string>> {
  const out: Partial<Record<Locale, string>> = {};
  for (const l of LOCALES) {
    const v = String(form.get(`${prefix}_${l}`) ?? '').trim();
    if (v) out[l] = v;
  }
  return out;
}

export function str(form: FormData, key: string): string {
  return String(form.get(key) ?? '').trim();
}

export function int(form: FormData, key: string, fallback = 0): number {
  const n = Number.parseInt(str(form, key), 10);
  return Number.isFinite(n) ? n : fallback;
}

/**
 * Spanish message for a failed admin action. The API returns stable codes
 * (catalog.sku_taken…) that are translated here; unknown codes fall back to
 * the API's own message.
 */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const p = err.problem;
    const known = p?.code ? errorTexts[p.code] : undefined;
    if (known) return known;
    if (p?.detail) return p.field ? `${p.detail} (${p.field})` : p.detail;
    return `Error ${err.status}`;
  }
  return err instanceof Error ? err.message : String(err);
}

/** Post/Redirect/Get: after a successful POST, reload the page with a notice. */
export function savedRedirect(url: URL, notice = 'saved'): string {
  const next = new URL(url);
  next.searchParams.set('notice', notice);
  return next.pathname + next.search;
}
