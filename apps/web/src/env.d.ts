declare namespace App {
  interface Locals {
    /** Locale from the URL prefix (/es, /en, /pt-br); es outside the store. */
    locale: import('./i18n/locales').Locale;
    /** Display currency chosen by the visitor (cookie), PEN by default. */
    currency: import('./i18n/locales').Currency;
  }
}
