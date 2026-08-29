"use client"

import {Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {ArrowLeft} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense} from "react"

import {SubscriptionStatusBadge} from "@/components/console/SubscriptionStatusBadge"
import {Timeline} from "@/components/console/Timeline"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {statusOf} from "@/lib/api/client"
import {consoleKeys, getConsoleCustomer} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"
import {shortDate} from "@/lib/format"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/**
 * C7 — one customer: who they are, what they are on, and what has been done to
 * the record.
 *
 * The tax id is masked and stays masked. Revealing it in full is an audited
 * action the API does not yet expose (`RecordTaxIDAccess` exists in the
 * repository and no route calls it), and shipping a button that quietly showed
 * the whole CPF without writing that row would be worse than not having one.
 */
export default function ConsoleCustomerPage() {
  return (
    <Suspense fallback={<DetailSkeleton/>}>
      <Detail/>
    </Suspense>
  )
}

function Detail() {
  const id = useSearchParams().get("id") ?? ""
  const mode = useMode()

  const query = useQuery({
    queryKey: consoleKeys.customer(mode, id),
    queryFn: () => getConsoleCustomer(id, mode),
    enabled: id !== "",
  })

  useDocumentTitle(query.data?.customer.name ?? null)

  if (id === "" || (query.isError && statusOf(query.error) === 404)) return <NotFound/>
  if (query.isPending) return <DetailSkeleton/>
  if (query.isError) {
    return (
      <div className="space-y-6">
        <BackLink/>
        <ErrorBlock error={query.error} onRetry={query.refetch}/>
      </div>
    )
  }

  const {customer, subscriptions, timeline} = query.data

  return (
    <div className="space-y-8">
      <header className="space-y-3">
        <BackLink/>
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
          {customer.name}
        </h1>
        {customer.anonymized && (
          <p className="text-sm text-muted-foreground">
            Este cadastro foi anonimizado a pedido do titular. As faturas emitidas continuam
            existindo — um documento não some porque alguém pediu para ser esquecido.
          </p>
        )}
      </header>

      <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 text-sm sm:grid-cols-3">
        <Fact label="E-mail" value={customer.email || "—"}/>
        <Fact label="CPF/CNPJ" value={customer.tax_id_masked || "—"} numeric/>
        <Fact label="Referência externa" value={customer.external_ref || "—"}/>
      </dl>

      <section aria-labelledby="assinaturas" className="space-y-3">
        <h2 id="assinaturas" className="text-sm font-medium text-muted-foreground">
          Assinaturas
        </h2>
        {subscriptions.length === 0 ? (
          <p className="text-sm text-muted-foreground">Este cliente não tem assinaturas.</p>
        ) : (
          <ul className="divide-y divide-border border-y border-border">
            {subscriptions.map(sub => (
              <li key={sub.id} className="flex flex-wrap items-center justify-between gap-3 py-2">
                <div className="flex flex-wrap items-center gap-3">
                  <Link
                    href={`/console/subscription?id=${sub.id}`}
                    className="text-sm text-brand-600 underline-offset-4 hover:underline"
                  >
                    {sub.id}
                  </Link>
                  <SubscriptionStatusBadge
                    status={sub.status}
                    endingAtPeriodEnd={sub.cancel_at_period_end}
                  />
                </div>
                <span data-numeric className="text-xs text-muted-foreground">
                  {shortDate(sub.current_period.start)} – {shortDate(sub.current_period.end)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <Timeline entries={timeline}/>
    </div>
  )
}

function Fact({label, value, numeric}: { label: string; value: string; numeric?: boolean }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd data-numeric={numeric ? "" : undefined} className="truncate text-foreground">{value}</dd>
    </div>
  )
}

function NotFound() {
  return (
    <div className="space-y-6">
      <BackLink/>
      <div className="space-y-2">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
          Cliente não encontrado
        </h1>
        <p className="text-sm text-muted-foreground">
          Ele pode pertencer ao outro modo — confira se você está em Produção ou Teste.
        </p>
      </div>
    </div>
  )
}

function BackLink() {
  return (
    <Link
      href="/console/customers"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      Clientes
    </Link>
  )
}

function DetailSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-6 w-48"/>
      <Skeleton className="h-16 w-full"/>
      <Skeleton className="h-24 w-full"/>
    </div>
  )
}
