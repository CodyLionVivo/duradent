declare namespace App {
  interface Locals {
    /** Locale from the URL prefix (/es, /en, /pt-br); es outside the store. */
    locale: import('./i18n/locales').Locale;
    /** Display currency chosen by the visitor (cookie), PEN by default. */
    currency: import('./i18n/locales').Currency;
    /** API client acting as the visitor (session + client IP forwarded). */
    api: import('./lib/api/client').ApiClient;
    /** The signed-in account, or null for anonymous visitors. */
    user: import('./lib/api/client').Schemas['Account'] | null;
    /** The signed-in customer's billing profile, if filled in. */
    customer?: import('./lib/api/client').Schemas['Customer'] | null;
  }
}
