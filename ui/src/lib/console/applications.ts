import type {ApplyRequest, MonthlyVolume} from "@/lib/api/consoleTypes"
import type {FieldError} from "@/lib/errors"
import limits from "@/lib/limits.json"

/** The volume ranges, in the order the select lists them (labels: console.volume.*). */
export const MONTHLY_VOLUMES: MonthlyVolume[] = ["up_to_10k", "10k_to_50k", "50k_to_200k", "over_200k"]

export type ApplyFormValues = {
  company_id: string
  description: string
  website: string
  expected_monthly_volume: MonthlyVolume | ""
  contact_email: string
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const chars = (v: string) => [...v].length

/** An absolute https address with a host and no credentials (the API's rule). */
export function isHttpsURL(v: string): boolean {
  try {
    const u = new URL(v)
    return u.protocol === "https:" && u.hostname !== "" && u.username === "" && u.password === ""
  } catch {
    return false
  }
}

function text(field: string, v: string, min: number, max: number, out: FieldError[]) {
  const n = chars(v)
  if (n === 0) out.push({field, code: "required"})
  else if (n < min) out.push({field, code: "too_short", params: {min}})
  else if (n > max) out.push({field, code: "too_long", params: {max}})
}

/** The API's checks, run before the round trip; the server re-checks all of them. */
export function applyFieldErrors(v: ApplyFormValues): FieldError[] {
  const out: FieldError[] = []
  if (!v.company_id) out.push({field: "company_id", code: "required"})
  text("description", v.description.trim(), limits.minText.applicationDescription, limits.text.applicationDescription, out)
  const site = v.website.trim()
  if (site && (chars(site) > limits.text.website || !isHttpsURL(site))) out.push({field: "website", code: "invalid_url"})
  const mail = v.contact_email.trim()
  if (!mail) out.push({field: "contact_email", code: "required"})
  else if (!EMAIL.test(mail) || mail.length > limits.text.email) out.push({field: "contact_email", code: "invalid_email"})
  return out
}

export function toApplyRequest(v: ApplyFormValues): ApplyRequest {
  const site = v.website.trim()
  return {
    company_id: v.company_id,
    description: v.description.trim(),
    ...(site ? {website: site} : {}),
    ...(v.expected_monthly_volume ? {expected_monthly_volume: v.expected_monthly_volume} : {}),
    contact_email: v.contact_email.trim(),
  }
}

export function rejectReasonErrors(reason: string): FieldError[] {
  const out: FieldError[] = []
  text("reason", reason.trim(), limits.minText.rejectionReason, limits.text.reason, out)
  return out
}
