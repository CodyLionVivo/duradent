/**
 * Only same-site paths: never redirect to another origin (open redirect).
 * Rejects "//evil.com" and "/\evil.com", which browsers treat as absolute URLs.
 */
export function safeReturnPath(value: FormDataEntryValue | string | null): string {
  const path = typeof value === 'string' ? value : '';
  return path.startsWith('/') && !path.startsWith('//') && !path.startsWith('/\\') ? path : '/';
}

/**
 * The visitor's IP behind AWS's load balancer: the ALB appends the address it
 * saw to X-Forwarded-For, so only the last entry is trustworthy (anything
 * before it came from the client).
 */
export function proxiedClientIP(forwardedFor: string | null): string | undefined {
  const last = forwardedFor?.split(',').at(-1)?.trim();
  return last || undefined;
}
