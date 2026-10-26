/**
 * Only same-site paths: never redirect to another origin (open redirect).
 * Rejects "//evil.com" and "/\evil.com", which browsers treat as absolute URLs.
 */
export function safeReturnPath(value: FormDataEntryValue | string | null): string {
  const path = typeof value === 'string' ? value : '';
  return path.startsWith('/') && !path.startsWith('//') && !path.startsWith('/\\') ? path : '/';
}
