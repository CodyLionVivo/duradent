// Translation and locale-aware formatting. Every user-visible string comes
// from messages/<locale>.json; Spanish is the reference and the fallback.
import es from './messages/es.json';
import en from './messages/en.json';
import ptBR from './messages/pt-br.json';
import { CURRENCY, DEFAULT_LOCALE, INTL_LOCALE, TIME_ZONE, type Locale } from './locales';

export * from './locales';

type Messages = typeof es;

// Dotted paths to every string leaf: "nav.search", "admin.products.title"…
type Leaves<T, P extends string = ''> = {
  [K in keyof T & string]: T[K] extends string ? `${P}${K}` : Leaves<T[K], `${P}${K}.`>;
}[keyof T & string];

export type MessageKey = Leaves<Messages>;

type DeepPartial<T> = { [K in keyof T]?: T[K] extends string ? string : DeepPartial<T[K]> };

const catalogs: Record<Locale, DeepPartial<Messages>> = { es, en, 'pt-br': ptBR };

function lookup(messages: DeepPartial<Messages>, key: string): string | undefined {
  let node: unknown = messages;
  for (const part of key.split('.')) {
    if (typeof node !== 'object' || node === null) return undefined;
    node = (node as Record<string, unknown>)[part];
  }
  return typeof node === 'string' ? node : undefined;
}

export type Translate = (key: MessageKey, params?: Record<string, string | number>) => string;

/** Returns a translator for locale, falling back to Spanish for missing keys. */
export function useTranslations(locale: Locale): Translate {
  return (key, params) => {
    const template = lookup(catalogs[locale], key) ?? lookup(catalogs[DEFAULT_LOCALE], key) ?? key;
    if (!params) return template;
    return template.replace(/\{(\w+)\}/g, (match, name: string) =>
      name in params ? String(params[name]) : match,
    );
  };
}

const moneyFormats = new Map<string, Intl.NumberFormat>();

/** Formats a decimal string from the API ("85.00") in soles, in the visitor's locale. */
export function formatMoney(amount: string, locale: Locale): string {
  let fmt = moneyFormats.get(locale);
  if (!fmt) {
    fmt = new Intl.NumberFormat(INTL_LOCALE[locale], {
      style: 'currency',
      currency: CURRENCY,
      currencyDisplay: 'symbol',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    });
    moneyFormats.set(locale, fmt);
  }
  return fmt.format(Number(amount));
}

export function formatNumber(value: number, locale: Locale): string {
  return new Intl.NumberFormat(INTL_LOCALE[locale]).format(value);
}

export function formatDate(iso: string, locale: Locale): string {
  return new Intl.DateTimeFormat(INTL_LOCALE[locale], { dateStyle: 'medium', timeStyle: 'short', timeZone: TIME_ZONE }).format(new Date(iso));
}

/** A day without the time ("13 nov. 2026"), in Lima. */
export function formatDay(iso: string, locale: Locale): string {
  return new Intl.DateTimeFormat(INTL_LOCALE[locale], { dateStyle: 'long', timeZone: TIME_ZONE }).format(new Date(iso));
}

/** Builds a path inside a locale: localePath('en', '/catalog') → /en/catalog */
export function localePath(locale: Locale, path = '/'): string {
  const clean = path.startsWith('/') ? path : `/${path}`;
  return clean === '/' ? `/${locale}` : `/${locale}${clean}`;
}

/** Same page in another locale: swaps the first path segment. */
export function switchLocalePath(url: URL, to: Locale): string {
  const [, , ...rest] = url.pathname.split('/');
  const tail = rest.length ? `/${rest.join('/')}` : '';
  return `/${to}${tail}${url.search}`;
}
