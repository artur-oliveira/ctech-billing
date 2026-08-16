"use client"

import {Button, EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {ArrowRight, Receipt} from "lucide-react"
import Link from "next/link"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Money} from "@/components/portal/Money"
import {StatusBadge} from "@/components/portal/StatusBadge"
import {listInvoices, listSubscriptions, portalKeys} from "@/lib/api/portal"
import type {Invoice, Subscription} from "@/lib/api/types"
import {longDate, money} from "@/lib/format"

/**
 * P1 — Início. One screen, no chart.
 *
 * It answers three questions in falling order of urgency: is anything owed
 * right now, what is coming next, and what am I subscribed to. It composes
 * those from the two list endpoints rather than a dedicated summary route,
 * because a customer of this portal has a handful of invoices, not a page of
 * them, and a third endpoint would be a third thing to keep in agreement.
 *
 * There is no page title and there are no cards. "Suas cobranças" above a
 * screen the nav already labels "Início" put the largest type on the page onto
 * the one line carrying no information, and a bordered box around every block
 * meant the amount somebody owes and the plan they are subscribed to were
 * given the same weight. The answer to the screen's own question is the
 * heading; everything under it is quiet by comparison, which is the only way
 * anything can be loud.
 */
export default function HomePage() {
  const invoices = useQuery({queryKey: portalKeys.invoiceList, queryFn: () => listInvoices()})
  const subscriptions = useQuery({
    queryKey: portalKeys.subscriptions,
    queryFn: listSubscriptions,
  })

  const owed = (invoices.data?.data ?? []).filter(i => i.payable)
  const active = (subscriptions.data?.data ?? []).filter(s => s.cancelable)
  const next = nextCharge(active)

  const loading = invoices.isPending || subscriptions.isPending
  const bothFailed = invoices.isError && subscriptions.isError
  const empty =
    !loading &&
    !invoices.isError &&
    !subscriptions.isError &&
    owed.length === 0 &&
    active.length === 0

  if (loading) return <HomeSkeleton />

  return (
    <div className="space-y-12">
      {empty && (
        <EmptyState
          icon={<Receipt />}
          title="Nada para pagar por aqui"
          description="Quando você assinar um plano da CTech, a próxima cobrança e todas as faturas aparecem nesta tela."
        />
      )}

      {/* Errors are per block so that a failed subscriptions call still leaves
          the invoice somebody owes on screen. But when both fail — which on
          this page almost always means one cause, the connection — that becomes
          the same sentence printed twice with two identical buttons. One
          failure gets its own block; two get one. */}
      {bothFailed ? (
        <ErrorBlock
          error={invoices.error}
          onRetry={() => {
            void invoices.refetch()
            void subscriptions.refetch()
          }}
        />
      ) : (
        <>
          {invoices.isError && <ErrorBlock error={invoices.error} onRetry={invoices.refetch} />}

          {owed.length > 0 && <Pendencia invoices={owed} />}

          {!invoices.isError && !subscriptions.isError && owed.length === 0 && !empty && (
            <EmDia subscription={next} />
          )}

          {subscriptions.isError && (
            <ErrorBlock error={subscriptions.error} onRetry={subscriptions.refetch} />
          )}
        </>
      )}

      {active.length > 0 && <ActiveList subscriptions={active} />}
    </div>
  )
}

/** The soonest renewal among subscriptions that still have one. */
function nextCharge(subscriptions: Subscription[]): Subscription | undefined {
  return subscriptions
    .filter(s => s.renews_on)
    .sort((a, b) => a.renews_on!.localeCompare(b.renews_on!))[0]
}

/**
 * What is owed. The amount is the heading of the whole screen, because the
 * screen exists to answer "do I owe anything and how much".
 */
function Pendencia({invoices}: {invoices: Invoice[]}) {
  const [first, ...rest] = invoices
  const total = invoices.reduce((sum, i) => sum + i.amount_due, 0)

  return (
    <section className="space-y-6">
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          {invoices.length === 1 ? "Você tem uma fatura em aberto" : "Você tem faturas em aberto"}
        </p>
        <h1>
          <Money cents={first.amount_due} currency={first.currency} size="hero" />
        </h1>
        <StatusBadge state={first.state} tone={first.tone} />
        <p className="text-pretty text-sm text-muted-foreground">{first.description}</p>
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-3">
        <Button render={<Link href={`/invoice?id=${first.id}`} />}>
          Pagar {money(first.amount_due, first.currency)}
        </Button>
        {rest.length > 0 && (
          <Link
            href="/invoices"
            className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            e mais {rest.length} — {money(total - first.amount_due, first.currency)}
          </Link>
        )}
      </div>
    </section>
  )
}

/**
 * Nothing owed. The heading says so in words rather than showing a R$ 0,00,
 * which is the same fact rendered as an alarm.
 *
 * A metered subscription says its amount is not known yet instead of showing a
 * confident zero.
 */
function EmDia({subscription}: {subscription?: Subscription}) {
  return (
    <section className="space-y-3">
      <h1 className="text-3xl font-semibold tracking-[-0.02em] text-foreground">Tudo em dia</h1>
      {subscription ? (
        <p className="text-pretty text-sm text-muted-foreground">
          Próxima cobrança{" "}
          {subscription.metered
            ? "conforme o uso"
            : `de ${money(subscription.amount ?? 0, subscription.currency)}`}{" "}
          em {longDate(subscription.renews_on!)} · {subscription.description}
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">Nenhuma fatura em aberto.</p>
      )}
    </section>
  )
}

function ActiveList({subscriptions}: {subscriptions: Subscription[]}) {
  return (
    <section aria-labelledby="assinaturas" className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 id="assinaturas" className="text-sm font-medium text-muted-foreground">
          Suas assinaturas
        </h2>
        <Link
          href="/subscriptions"
          className="inline-flex items-center gap-1 text-sm text-brand-600 underline-offset-4 hover:underline"
        >
          Ver todas
          <ArrowRight aria-hidden className="size-3.5" />
        </Link>
      </div>
      <ul className="divide-y divide-border border-y border-border">
        {subscriptions.map(s => (
          <li key={s.id} className="flex items-center justify-between gap-4 py-4">
            <div className="min-w-0 space-y-1.5">
              <p className="truncate text-sm font-medium text-foreground">{s.description}</p>
              <StatusBadge state={s.state} tone={s.tone} />
            </div>
            {/* A metered line says so. Left blank it reads as a row that
                failed to load rather than a price that is not knowable yet. */}
            {s.metered ? (
              <span className="shrink-0 text-sm text-muted-foreground">Conforme o uso</span>
            ) : (
              s.amount != null && (
                <Money cents={s.amount} currency={s.currency} className="text-sm" />
              )
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}

function HomeSkeleton() {
  return (
    <div className="space-y-12" aria-busy>
      <div className="space-y-6">
        <div className="space-y-3">
          <Skeleton className="h-4 w-52" />
          <Skeleton className="h-9 w-48" />
          <Skeleton className="h-5 w-36 rounded-full" />
          <Skeleton className="h-4 w-64" />
        </div>
        <Skeleton className="h-11 w-44 rounded-lg" />
      </div>
      <div className="space-y-4">
        <Skeleton className="h-4 w-32" />
        <div className="space-y-3 border-y border-border py-4">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-5 w-24 rounded-full" />
        </div>
      </div>
    </div>
  )
}
