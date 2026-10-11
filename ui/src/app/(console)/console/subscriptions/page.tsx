"use client"

import {EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import type {TFunction} from "i18next"
import {Repeat} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"

import {SubscriptionStatusBadge} from "@/components/console/SubscriptionStatusBadge"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {consoleKeys, listConsoleSubscriptions} from "@/lib/api/console"
import type {ConsoleSubscriptionRow, Recurrence} from "@/lib/api/consoleTypes"
import {useConsoleScope} from "@/lib/console/scope"
import {shortDate} from "@/lib/format"

/**
 * C4 — the subscriptions.
 *
 * A row is named the way an operator asks about it: by its **plan** and its
 * **customer**, the same grammar as the invoice list. The ids are what the
 * server joins on, not what a person reads; the detail page still shows them.
 * Either name can be missing (the server could not read the record), and the
 * row then falls back to the id rather than going blank.
 *
 * The column that is not obvious is **Acesso**: it is the server's `entitled`,
 * not a re-derivation of the status. A trial is entitled and a past-due
 * subscription may still be, and whether it is is a policy the domain owns —
 * a console that recomputed it would eventually disagree with the entitlement
 * check every other CTech product calls.
 *
 * On a phone the table becomes a list (DESIGN.md "On a phone"): plan and status
 * on the first line, the customer on the second, then period, recurrence and
 * access as one muted line.
 */
export default function ConsoleSubscriptionsPage() {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const query = useQuery({
    queryKey: consoleKeys.subscriptions(scope),
    queryFn: () => listConsoleSubscriptions(scope),
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
        <>
          <ul
            aria-label={t("console.subscriptions.title")}
            className="divide-y divide-border border-y border-border sm:hidden"
          >
            {subscriptions.map(sub => (
              <li key={sub.id} className="space-y-1 py-3">
                {/* The status wraps under the plan rather than squeezing it
                    when a row carries two badges. */}
                <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-1">
                  <PlanLink sub={sub}/>
                  <span>
                    <SubscriptionStatusBadge status={sub.status} endingAtPeriodEnd={sub.cancel_at_period_end}/>
                  </span>
                </div>
                <p className="truncate text-sm text-foreground">
                  {sub.customer_name || <span className="text-muted-foreground">{sub.customer_id}</span>}
                </p>
                <p className="text-xs text-muted-foreground">
                  <span data-numeric>
                    {shortDate(sub.current_period.period_start)} – {shortDate(sub.current_period.period_end)}
                  </span>
                  {" • "}
                  {recurrenceLabel(t, sub.recurrence)}
                  {" • "}
                  <span className={sub.entitled ? "text-foreground" : undefined}>
                    {sub.entitled ? t("console.subscriptions.accessAllowed") : t("console.subscriptions.accessBlocked")}
                  </span>
                </p>
              </li>
            ))}
          </ul>

          <div className="hidden overflow-x-auto sm:block">
            <table className="w-full min-w-[46rem] border-collapse text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th scope="col" className="py-2 pr-4 font-medium">{t("console.subscriptions.plan")}</th>
                  <th scope="col" className="py-2 pr-4 font-medium">{t("console.subscriptions.customer")}</th>
                  <th scope="col" className="w-56 py-2 pr-4 font-medium">{t("console.subscriptions.status")}</th>
                  <th scope="col" className="w-52 py-2 pr-4 font-medium">{t("console.subscriptions.period")}</th>
                  <th scope="col" className="w-32 py-2 pr-4 font-medium">{t("console.subscriptions.recurrence")}</th>
                  <th scope="col" className="w-24 py-2 font-medium">{t("console.subscriptions.access")}</th>
                </tr>
              </thead>
              <tbody>
                {subscriptions.map(sub => (
                  <tr key={sub.id} className="border-b border-border last:border-0 hover:bg-surface">
                    <td className="max-w-0 truncate py-2 pr-4">
                      <PlanLink sub={sub}/>
                    </td>
                    <td className="max-w-0 truncate py-2 pr-4">
                      <Link
                        href={`/console/customer?id=${sub.customer_id}`}
                        className={`underline-offset-4 hover:underline ${
                          sub.customer_name ? "text-foreground" : "text-muted-foreground hover:text-foreground"
                        }`}
                      >
                        {sub.customer_name || sub.customer_id}
                      </Link>
                    </td>
                    <td className="py-2 pr-4">
                      <SubscriptionStatusBadge
                        status={sub.status}
                        endingAtPeriodEnd={sub.cancel_at_period_end}
                      />
                    </td>
                    <td data-numeric className="py-2 pr-4 text-muted-foreground">
                      {shortDate(sub.current_period.period_start)} – {shortDate(sub.current_period.period_end)}
                    </td>
                    <td className="py-2 pr-4 text-muted-foreground">
                      {recurrenceLabel(t, sub.recurrence)}
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
        </>
      )}
    </div>
  )
}

/** The row's name and its way in: the plan, or the id when the plan could not
 *  be read. */
function PlanLink({sub}: { sub: ConsoleSubscriptionRow }) {
  return (
    <Link
      href={`/console/subscription?id=${sub.id}`}
      className="font-medium text-brand-600 underline-offset-4 hover:underline"
    >
      {sub.plan_name || sub.id}
    </Link>
  )
}

function recurrenceLabel(t: TFunction, recurrence: Recurrence): string {
  return recurrence.count === 1
    ? t(`console.interval.${recurrence.interval}`, {defaultValue: recurrence.interval})
    : t(`console.every.${recurrence.interval}`, {count: recurrence.count, defaultValue: recurrence.interval})
}

function RowsSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2].map(i => <Skeleton key={i} className="h-8 w-full"/>)}
    </div>
  )
}
