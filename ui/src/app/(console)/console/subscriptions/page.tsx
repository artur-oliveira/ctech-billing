"use client"

import {EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Repeat} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"

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
  const {t} = useTranslation()
  const mode = useMode()
  const query = useQuery({
    queryKey: consoleKeys.subscriptions(mode),
    queryFn: () => listConsoleSubscriptions(mode),
  })

  const subscriptions = query.data?.data ?? []

  return (
    <div className="space-y-6">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("console.subscriptions.title")}</h1>

      {query.isPending && <RowsSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && subscriptions.length === 0 && (
        <EmptyState
          icon={<Repeat/>}
          title={t("console.subscriptions.empty")}
        />
      )}

      {subscriptions.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[44rem] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.subscriptions.subscription")}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.subscriptions.customer")}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.subscriptions.status")}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.subscriptions.period")}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.subscriptions.recurrence")}</th>
                <th scope="col" className="py-2 font-medium">{t("console.subscriptions.access")}</th>
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
                      ? t(`console.interval.${sub.recurrence.interval}`, {defaultValue: sub.recurrence.interval})
                      : t(`console.every.${sub.recurrence.interval}`, {count: sub.recurrence.count, defaultValue: sub.recurrence.interval})}
                  </td>
                  <td className="py-2">
                    {sub.entitled ? (
                      <span className="text-foreground">{t("console.subscriptions.allowed")}</span>
                    ) : (
                      <span className="text-muted-foreground">{t("console.subscriptions.blocked")}</span>
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

function RowsSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2].map(i => <Skeleton key={i} className="h-8 w-full"/>)}
    </div>
  )
}
