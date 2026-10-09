"use client"

import {Button, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ArrowLeft, Eye} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {SubscriptionStatusBadge} from "@/components/console/SubscriptionStatusBadge"
import {Timeline} from "@/components/console/Timeline"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor, statusOf} from "@/lib/api/client"
import {consoleKeys, getConsoleCustomer, revealTaxID} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"
import {shortDate} from "@/lib/format"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/**
 * C7 — one customer: who they are, what they are on, and what has been done to
 * the record.
 *
 * The tax id is masked until somebody deliberately reveals it, and revealing
 * writes an audit row naming who looked. That is what makes the masking
 * everywhere else mean something: without the record, a data-subject request
 * asking "who has seen my CPF" would have no honest answer.
 *
 * The revealed value lives in this component's state and nowhere else — not in
 * the query cache, which the mode switch and a refetch both outlive. Leaving
 * the console and coming back re-asks, and re-asking writes another row, which
 * is the correct cost of looking twice.
 */
export default function ConsoleCustomerPage() {
  return (
    <Suspense fallback={<DetailSkeleton/>}>
      <Detail/>
    </Suspense>
  )
}

function Detail() {
  const {t} = useTranslation()
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
          <p className="text-sm text-muted-foreground">{t("console.customer.anonymized")}</p>
        )}
      </header>

      <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 text-sm sm:grid-cols-3">
        <Fact label={t("console.customer.email")} value={customer.email || "—"}/>
        <TaxID customerId={customer.id} masked={customer.tax_id_masked}/>
        <Fact label={t("console.customer.externalRef")} value={customer.external_ref || "—"}/>
      </dl>

      <section aria-labelledby="assinaturas" className="space-y-3">
        <h2 id="assinaturas" className="text-sm font-medium text-muted-foreground">
          {t("console.customer.subscriptions")}
        </h2>
        {subscriptions.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("console.customer.noSubscriptions")}</p>
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

/**
 * The masked tax id, and the one action that unmasks it.
 *
 * The button disappears once revealed rather than becoming "ocultar": hiding it
 * again would suggest the reveal can be taken back, and the audit row says
 * otherwise.
 */
function TaxID({customerId, masked}: { customerId: string; masked?: string }) {
  const {t} = useTranslation()
  const mode = useMode()
  const queryClient = useQueryClient()
  const [full, setFull] = useState<string | null>(null)

  const reveal = useMutation({
    mutationFn: () => revealTaxID(customerId, mode),
    onSuccess: value => {
      setFull(value)
      // The timeline below now has a row it did not have a moment ago, and the
      // operator should see their own name in it.
      void queryClient.invalidateQueries({queryKey: consoleKeys.customer(mode, customerId)})
    },
    onError: error => toast.error(messageFor(error)),
  })

  if (!masked) return <Fact label={t("console.customer.taxId")} value="—"/>

  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{t("console.customer.taxId")}</dt>
      <dd className="flex items-center gap-2">
        <span data-numeric className="text-foreground">{full ?? masked}</span>
        {full === null && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => reveal.mutate()}
            disabled={reveal.isPending}
          >
            <Eye aria-hidden className="size-3.5"/>
            {reveal.isPending ? t("console.customer.revealing") : t("console.customer.reveal")}
          </Button>
        )}
      </dd>
      {full === null && (
        <p className="text-xs text-muted-foreground">{t("console.customer.revealNote")}</p>
      )}
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
  const {t} = useTranslation()
  return (
    <div className="space-y-6">
      <BackLink/>
      <div className="space-y-2">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
          {t("console.customer.notFound")}
        </h1>
        <p className="text-sm text-muted-foreground">
          {t("console.wrongMode")}
        </p>
      </div>
    </div>
  )
}

function BackLink() {
  const {t} = useTranslation()
  return (
    <Link
      href="/console/customers"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      {t("console.customer.back")}
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
