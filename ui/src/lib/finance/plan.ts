import type {PlanPrice, PlanState} from "@/lib/api/financeTypes"
import type {Cents} from "@/lib/api/types"

export type PlanKey = string

export interface PlanOption {
  key: PlanKey
  priceIds: string[]
  isDefault: boolean
  monthly: Cents | null
  metered: {meter: string; unitAmount: Cents; included: number}[]
  quotaSpaces: number
  quotaPeople: number
}

/**
 * A quota from price metadata (ADR 0008: billing's Go side never reads it; this
 * screen does). -1 is unlimited; missing or not an integer is 0 — the same rule
 * as ctech-account, so a catalogue mistake shows as "no room", never "unlimited".
 */
export function quota(v: string | undefined): number {
  if (v === "-1") return Infinity
  return v !== undefined && /^\d+$/.test(v) ? Number(v) : 0
}

export function planOptions(state: PlanState): PlanOption[] {
  const groups = new Map<PlanKey, PlanPrice[]>()
  for (const p of state.prices) {
    const key = p.metadata.plan ?? p.id
    groups.set(key, [...(groups.get(key) ?? []), p])
  }
  const options = [...groups.entries()].map(([key, prices]): PlanOption => {
    const fixed = prices.find(p => p.type === "fixed")
    const withQuota = prices.find(p => p.metadata.quota_spaces !== undefined) ?? prices[0]
    const isDefault = prices.some(p => p.id === state.default_price_id)
    return {
      key,
      priceIds: isDefault ? [] : prices.map(p => p.id),
      isDefault,
      monthly: fixed ? fixed.unit_amount : null,
      metered: prices.filter(p => p.type === "metered").map(p => ({meter: p.meter, unitAmount: p.unit_amount, included: p.included_quantity})),
      quotaSpaces: quota(withQuota.metadata.quota_spaces),
      quotaPeople: quota(withQuota.metadata.quota_people_per_space),
    }
  })
  const rank = (o: PlanOption) => (o.isDefault ? -1 : o.monthly === null ? Number.MAX_SAFE_INTEGER : o.monthly)
  return options.sort((a, b) => rank(a) - rank(b))
}

export function currentOption(state: PlanState, options: PlanOption[]): PlanOption {
  const ids = state.subscription?.price_ids ?? []
  return options.find(o => !o.isDefault && o.priceIds.length > 0 && o.priceIds.every(id => ids.includes(id)))
    ?? options.find(o => o.isDefault) ?? options[0]
}

export interface OverLimit {
  resource: "spaces" | "people"
  used: number
  limit: number
  spaceName?: string
}

/** D6: what the person has beyond the plan. Nothing on unknown usage. */
export function overLimits(state: PlanState, current: PlanOption): OverLimit[] {
  if (state.usage.unavailable) return []
  const out: OverLimit[] = []
  const owned = state.usage.spaces.length
  if (owned > current.quotaSpaces) out.push({resource: "spaces", used: owned, limit: current.quotaSpaces})
  for (const s of state.usage.spaces) {
    const people = s.people + s.pending_invitations
    if (people > current.quotaPeople) out.push({resource: "people", used: people, limit: current.quotaPeople, spaceName: s.display_name})
  }
  return out
}

/** The plan a scheduled change moves to, and when (decision 5a). */
export function pendingOption(state: PlanState, options: PlanOption[]): {option: PlanOption; at: string} | null {
  const pc = state.subscription?.pending_change
  if (!pc) return null
  const option = options.find(o => !o.isDefault && o.priceIds.length > 0 && o.priceIds.every(id => pc.price_ids.includes(id)))
  return option ? {option, at: pc.at} : null
}

/** The cheapest fixed plan Sob demanda's projection already costs more than. */
export function suggestBasic(state: PlanState, options: PlanOption[], current: PlanOption): PlanOption | null {
  if (current.monthly !== null || current.isDefault) return null
  const basic = options.find(o => o.key === "basic")
  return basic && basic.monthly !== null && state.projected_ondemand_cents > basic.monthly ? basic : null
}
