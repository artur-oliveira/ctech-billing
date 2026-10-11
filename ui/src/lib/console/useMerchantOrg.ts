"use client"

import {useSyncExternalStore} from "react"

import {getMerchantOrg, onMerchantOrgChange} from "@/lib/console/merchant"

/** The stored organization as React state (the useMode pattern). */
export function useMerchantOrg(): string | null {
  return useSyncExternalStore(onMerchantOrgChange, getMerchantOrg, () => null)
}
