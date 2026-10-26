// Locales the store is published in. The code is also the URL prefix and the
// value the API accepts as ?locale=. Keep in sync with services/api/internal/kernel/locale.
export const LOCALES = ['es', 'en', 'pt-br'] as const;
export type Locale = (typeof LOCALES)[number];
export const DEFAULT_LOCALE: Locale = 'es';

export function isLocale(value: string | undefined): value is Locale {
  return value !== undefined && (LOCALES as readonly string[]).includes(value);
}

/** BCP 47 tag for Intl formatting; Spanish uses Peruvian conventions. */
export const INTL_LOCALE: Record<Locale, string> = {
  es: 'es-PE',
  en: 'en-US',
  'pt-br': 'pt-BR',
};

/** Value for <html lang> and hreflang. */
export const HTML_LANG: Record<Locale, string> = {
  es: 'es',
  en: 'en',
  'pt-br': 'pt-BR',
};

/** Native language names, shown in the language switcher. */
export const LOCALE_NAMES: Record<Locale, string> = {
  es: 'Español',
  en: 'English',
  'pt-br': 'Português (Brasil)',
};

/** The store sells only in soles. */
export const CURRENCY = 'PEN' as const;
export type Currency = typeof CURRENCY;

/** Picks the best supported locale from an Accept-Language header. */
export function negotiateLocale(header: string | null): Locale {
  if (!header) return DEFAULT_LOCALE;
  const ranked = header
    .split(',')
    .map((part) => {
      const [tag, q] = part.trim().toLowerCase().split(';q=');
      return { tag, q: q ? Number(q) : 1 };
    })
    .sort((a, b) => b.q - a.q);
  for (const { tag } of ranked) {
    if (tag.startsWith('pt')) return 'pt-br';
    if (tag.startsWith('en')) return 'en';
    if (tag.startsWith('es')) return 'es';
  }
  return DEFAULT_LOCALE;
}
