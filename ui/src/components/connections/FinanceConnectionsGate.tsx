"use client"

import {EmptyState, Skeleton} from "@aoctech/ui"
import type {ReactNode} from "react"
import {useTranslation} from "react-i18next"

import type {ConnectionsScope} from "@/lib/api/connectionsTypes"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"

import {useFinanceConnectionsScope} from "./useConnectionsScope"

/**
 * Finanças' bank connections are an organization's, for whoever may configure
 * its space (owner, admin). Anyone else gets the not-found view, the same
 * answer the API gives (presentation only; the server decides).
 */
export function FinanceConnectionsGate({children}: {children: (scope: ConnectionsScope) => ReactNode}) {
  const {t} = useTranslation()
  const scope = useFinanceConnectionsScope()
  const {can, loading} = useFinanceSpaces()
  if (loading) return <div className="space-y-3" aria-busy><Skeleton className="h-6 w-48"/><Skeleton className="h-4 w-full"/></div>
  if (!scope || !can("finance.configure")) {
    return <EmptyState title={t("errors.code.resource_not_found")} description={t("connections.intro")}/>
  }
  return <>{children(scope)}</>
}
