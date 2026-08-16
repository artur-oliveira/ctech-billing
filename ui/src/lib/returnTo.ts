/**
 * Where to send a reader after an interruption, from a path that arrived in a
 * query string.
 *
 * Its own module because it is a security decision, not a formatting one: this
 * is the function that stops `?from=https://evil.example` from turning
 * /maintenance into an open redirect. That matters more here than on most
 * pages — people arrive at this app from a payment e-mail, which is the same
 * channel a phishing chain uses, and a CTech-branded page that forwards
 * anywhere is a very good first hop.
 *
 * Only a same-site absolute path is honoured. `//evil.example` is rejected
 * along with everything else that is not one: the browser reads a
 * protocol-relative URL as a different origin, so `startsWith("/")` alone is
 * not the check it looks like.
 */
export function safeReturn(from: string | null | undefined, fallback: string): string {
  if (!from) return fallback
  if (!from.startsWith("/")) return fallback
  if (from.startsWith("//")) return fallback
  // A backslash is a forward slash to some URL parsers and not to others, which
  // is exactly the disagreement a bypass lives in.
  if (from.startsWith("/\\")) return fallback
  return from
}
