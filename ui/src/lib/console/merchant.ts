"use client"

import type {MerchantOrganization, MerchantStatus} from "@/lib/api/consoleTypes"
import type {Mode} from "@/lib/console/mode"

/**
 * The organization the billing half of the console acts for (ADR 0028): an
 * account organization id. It is a REQUEST, the `X-Billing-Space` header, and
 * the server re-authorizes it on every call. Stored apart from the finance
 * space: Finanças may be personal, billing never is.
 */
export type ConsoleScope = {mode: Mode; org: string}

const KEY = "ctech-billing-console-organization"
const EVENT = "ctech-console-organization"
const ORG_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

export function getMerchantOrg(): string | null {
  if (typeof window === "undefined") return null
  try {
    const raw = window.localStorage.getItem(KEY)
    return raw && ORG_ID.test(raw) ? raw : null
  } catch {
    return null
  }
}

export function setMerchantOrg(id: string) {
  try {
    window.localStorage.setItem(KEY, id)
  } catch {
    // Site data blocked: the switch still works for this page.
  }
  window.dispatchEvent(new CustomEvent(EVENT))
}

/** Subscribes to changes, including another tab's. */
export function onMerchantOrgChange(fn: () => void): () => void {
  window.addEventListener(EVENT, fn)
  window.addEventListener("storage", fn)
  return () => {
    window.removeEventListener(EVENT, fn)
    window.removeEventListener("storage", fn)
  }
}

/** Whether the organization has a tenant in this mode. Rejected keeps its test
 *  tenant; suspended keeps both (reads still work). */
export function tenantInMode(status: MerchantStatus, mode: Mode): boolean {
  switch (status) {
    case "live":
    case "suspended":
      return true
    case "test":
    case "rejected":
      return mode === "test"
    default:
      return false
  }
}

/** The organization to show: the stored one while it is still listed, else the
 *  first with a tenant in this mode, else the first. */
export function pickMerchantOrg(orgs: MerchantOrganization[], stored: string | null, mode: Mode): MerchantOrganization | null {
  return orgs.find(o => o.organization_id === stored)
    ?? orgs.find(o => tenantInMode(o.merchant_status, mode))
    ?? orgs[0]
    ?? null
}
