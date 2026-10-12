"use client"

import type {ConnectionsScope} from "@/lib/api/connectionsTypes"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"

/**
 * Finanças' connections scope: the selected space, when it is an
 * organization's. A personal space connects nothing (BYOK spec § 3.1), so it
 * is null and the page shows the not-found view. The console's scope is its
 * own `useConsoleScope()`, already `{mode, org}`.
 */
export function useFinanceConnectionsScope(): ConnectionsScope | null {
  const {mode, space} = useFinanceCtx()
  return space.kind === "organization" ? {org: space.organizationId, mode} : null
}
