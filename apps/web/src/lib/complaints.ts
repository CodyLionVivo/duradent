// Helpers for the Libro de Reclamaciones pages.
import { TIME_ZONE } from '../i18n/locales';

/** The calendar day (YYYY-MM-DD) of an instant, in Lima. */
function limaDay(d: Date): number {
  return Date.parse(new Intl.DateTimeFormat('en-CA', { timeZone: TIME_ZONE, year: 'numeric', month: '2-digit', day: '2-digit' }).format(d));
}

/**
 * Calendar days from today (in Lima) until the deadline's day: 0 on the last
 * day, negative once it has passed (deadlines end at 23:59 in Lima).
 */
export function daysLeft(dueISO: string, now: Date): number {
  return Math.round((limaDay(new Date(dueISO)) - limaDay(now)) / 86_400_000);
}
