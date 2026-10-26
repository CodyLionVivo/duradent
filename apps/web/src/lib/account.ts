// Helpers for the customer account pages.
import { ApiError } from './api/client';
import type { MessageKey, Translate } from '../i18n';
import { safeReturnPath } from './urls';

/** Departments of Peru with INEI codes; keep in sync with identity/domain/address.go. */
export const DEPARTMENTS: [code: string, name: string][] = [
  ['01', 'Amazonas'], ['02', 'Áncash'], ['03', 'Apurímac'], ['04', 'Arequipa'], ['05', 'Ayacucho'],
  ['06', 'Cajamarca'], ['07', 'Callao'], ['08', 'Cusco'], ['09', 'Huancavelica'], ['10', 'Huánuco'],
  ['11', 'Ica'], ['12', 'Junín'], ['13', 'La Libertad'], ['14', 'Lambayeque'], ['15', 'Lima'],
  ['16', 'Loreto'], ['17', 'Madre de Dios'], ['18', 'Moquegua'], ['19', 'Pasco'], ['20', 'Piura'],
  ['21', 'Puno'], ['22', 'San Martín'], ['23', 'Tacna'], ['24', 'Tumbes'], ['25', 'Ucayali'],
];

export function departmentName(code: string): string {
  return DEPARTMENTS.find(([c]) => c === code)?.[1] ?? code;
}

/**
 * Message for an API error in the visitor's language. Known codes
 * (identity.email_taken…) are translated; anything else gets a generic text,
 * never raw server messages.
 */
export function apiErrorText(t: Translate, err: unknown): string {
  const code = err instanceof ApiError ? err.problem?.code : typeof err === 'string' ? err : undefined;
  if (code) {
    const key = `codes.${code}` as MessageKey;
    const text = t(key);
    if (text !== key) return text;
  }
  return t('account.errors.generic');
}

/** Where to go after signing in: a same-site path, or the account page. */
export function afterSignIn(next: string | null, fallback: string): string {
  const path = safeReturnPath(next);
  return path === '/' ? fallback : path;
}
