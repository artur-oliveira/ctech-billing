"use client"

import {EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Repeat} from "lucide-react"
import Link from "next/link"

import {SubscriptionStatusBadge} from "@/components/console/SubscriptionStatusBadge"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {consoleKeys, listConsoleSubscriptions} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"
import {shortDate} from "@/lib/format"

/**
 * C4 — the subscriptions.
 *
 * The column that is not obvious is **Acesso**: it is the server's `entitled`,
 * not a re-derivation of the status. A trial is entitled and a past-due
 * subscription may still be, and whether it is is a policy the domain owns —
 * a console that recomputed it would eventually disagree with the entitlement
 * check every other CTech product calls.
 */
export default function ConsoleSubscriptionsPage() {
  const mode = useMode()
  const query = useQuery({
    queryKey: consoleKeys.subscriptions(mode),
    queryFn: () => listConsoleSubscriptions(mode),
  })

  const subscriptions = query.data?.data ?? []

  return (
    <div className="space-y-6">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Assinaturas</h1>

      {query.isPending && <RowsSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && subscriptions.length === 0 && (
        <EmptyState
          icon={<Repeat/>}
          title="Nenhuma assinatura"
          description="Assinaturas criadas por uma integração ou pelo console aparecem aqui."
        />
      )}

      {subscriptions.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[44rem] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th scope="col" className="py-2 pr-4 font-medium">Assinatura</th>
                <th scope="col" className="py-2 pr-4 font-medium">Cliente</th>
                <th scope="col" className="py-2 pr-4 font-medium">Situação</th>
                <th scope="col" className="py-2 pr-4 font-medium">Período atual</th>
                <th scope="col" className="py-2 pr-4 font-medium">Recorrência</th>
                <th scope="col" className="py-2 font-medium">Acesso</th>
              </tr>
            </thead>
            <tbody>
              {subscriptions.map(sub => (
                <tr key={sub.id} className="border-b border-border last:border-0 hover:bg-surface">
                  <td className="py-2 pr-4">
                    <Link
                      href={`/console/subscription?id=${sub.id}`}
                      className="text-brand-600 underline-offset-4 hover:underline"
                    >
                      {sub.id}
                    </Link>
                  </td>
                  <td className="py-2 pr-4">
                    <Link
                      href={`/console/customer?id=${sub.customer_id}`}
                      className="text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                    >
                      {sub.customer_id}
                    </Link>
                  </td>
                  <td className="py-2 pr-4">
                    <SubscriptionStatusBadge
                      status={sub.status}
                      endingAtPeriodEnd={sub.cancel_at_period_end}
                    />
                  </td>
                  <td data-numeric className="py-2 pr-4 text-muted-foreground">
                    {shortDate(sub.current_period.start)} – {shortDate(sub.current_period.end)}
                  </td>
                  <td className="py-2 pr-4 text-muted-foreground">
                    {sub.recurrence.count === 1
                      ? intervalLabel(sub.recurrence.interval)
                      : `a cada ${sub.recurrence.count} ${intervalLabel(sub.recurrence.interval)}`}
                  </td>
                  <td className="py-2">
                    {sub.entitled ? (
                      <span className="text-foreground">Liberado</span>
                    ) : (
                      <span className="text-muted-foreground">Bloqueado</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function intervalLabel(interval: string): string {
  return {month: "mensal", year: "anual", week: "semanal", day: "diária"}[interval] ?? interval
}

function RowsSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2].map(i => <Skeleton key={i} className="h-8 w-full"/>)}
    </div>
  )
}
