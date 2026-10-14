import type {ProviderGuide} from "@/lib/api/connectionsTypes"

/**
 * The guide permissions (by key) that grant any of these scopes, in the guide's
 * order, so a scope the bank refused is told back as the permission to tick. A
 * scope no permission grants is kept as is.
 */
export function permissionsForScopes(guide: ProviderGuide | undefined, scopes: string[]): {keys: string[]; unknown: string[]} {
  const keys: string[] = []
  const known = new Set<string>()
  for (const a of guide?.activities ?? []) {
    for (const p of a.permissions) {
      const hit = (p.scopes ?? []).filter(s => scopes.includes(s))
      if (hit.length === 0) continue
      keys.push(p.key)
      hit.forEach(s => known.add(s))
    }
  }
  return {keys, unknown: scopes.filter(s => !known.has(s))}
}
