/**
 * Where to land after sign-in, if it is a path on this site. Anything else —
 * a scheme, a protocol-relative "//host", a backslash browsers read as a slash,
 * a control character — goes to the dashboard: the value comes from the URL a
 * person was sent to, and an attacker can craft that URL.
 */
export function safeReturnTo(path: string | null | undefined): string {
  if (!path || !path.startsWith("/") || path[1] === "/" || path[1] === "\\" || /[\u0000-\u001f]/.test(path)) {
    return "/dashboard"
  }
  return path
}
