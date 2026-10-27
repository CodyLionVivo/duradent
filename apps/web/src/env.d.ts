declare namespace App {
  interface Locals {
    /** Locale from the URL prefix (/es, /en, /pt-br); es outside the store. */
    locale: import('./i18n/locales').Locale;
    /** API client acting as the visitor (session or cart key, client IP). */
    api: import('./lib/api/client').ApiClient;
    /** The visitor's IP, forwarded to the API. */
    clientIP?: string;
    /** The signed-in account, or null for anonymous visitors. */
    user: import('./lib/api/client').Schemas['Account'] | null;
    /** The signed-in customer's billing profile, if filled in. */
    customer?: import('./lib/api/client').Schemas['Customer'] | null;
    /** Units in the visitor's cart (for the header). */
    cartCount: number;
  }
}
