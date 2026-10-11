"use client"

import {useQuery} from "@tanstack/react-query"

import {consoleKeys, getConsoleSession} from "@/lib/api/console"
import {useConsoleScope} from "@/lib/console/scope"

/**
 * Whether this screen should offer to ADD integration surface (connect, create,
 * test). A suspended live tenant may only remove or secure (scope decision 1);
 * the server refuses anyway with `tenant_suspended`, this only avoids offering
 * a button that cannot work. Display data, never authority.
 */
export function useAddsRefused(): boolean {
  const scope = useConsoleScope()
  const session = useQuery({queryKey: consoleKeys.session(), queryFn: getConsoleSession})
  const org = session.data?.organizations.find(o => o.organization_id === scope.org)
  return scope.mode === "live" && org?.merchant_status === "suspended"
}
