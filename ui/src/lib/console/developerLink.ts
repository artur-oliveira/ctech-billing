/**
 * ctech-account's developer page, opened on "new client" with billing's
 * machine scopes ticked (ctech-account `?new=1&scopes=`). The scopes come from
 * the API's own list (`machine_scopes`), never from a copy here. The merchant
 * creates the client there and gives billing only its id: the secret never
 * passes through billing (spec D5).
 */
const ACCOUNTS = process.env.NEXT_PUBLIC_CTECH_CLIENT_URL || "https://accounts.aoctech.app"

export function developerClientURL(scopes: string[]): string {
  const u = new URL("/account/oauth-clients", ACCOUNTS)
  u.searchParams.set("new", "1")
  u.searchParams.set("scopes", scopes.join(","))
  return u.toString()
}
