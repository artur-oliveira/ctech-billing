"use client"

import {Button, Modal, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ArrowLeft} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {InvoiceRow} from "@/components/portal/InvoiceRow"
import {Money} from "@/components/portal/Money"
import {StatusBadge} from "@/components/portal/StatusBadge"
import {messageFor, statusOf} from "@/lib/api/client"
import {cancelSubscription, getSubscription, portalKeys} from "@/lib/api/portal"
import {longDate, period} from "@/lib/format"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/**
 * P5 — one subscription: what it is, what it costs, when it renews, what it has
 * charged so far, and how to end it.
 *
 * The list already answers the first three for every plan at once, so this
 * screen exists for the two it cannot: the history, and how long this has been
 * running. Both are the same question a person actually arrives with — "quanto
 * eu já paguei por isso, e desde quando" — and answering it here is what keeps
 * them off the invoice list counting rows.
 *
 * `?id=` and not `/[id]`, for the reason every detail route here does it: the
 * production build is `output: "export"` (ADR 0013), and a dynamic segment
 * would need every subscription enumerated at build time.
 */
export default function SubscriptionPage() {
  return (
    <Suspense fallback={<DetailSkeleton/>}>
      <Detail/>
    </Suspense>
  )
}

function Detail() {
  const {t} = useTranslation()
  const id = useSearchParams().get("id") ?? ""
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState(false)

  const query = useQuery({
    queryKey: portalKeys.subscription(id),
    queryFn: () => getSubscription(id),
    enabled: id !== "",
  })

  const cancel = useMutation({
    mutationFn: () => cancelSubscription(id),
    onSuccess: fresh => {
      // The response is the subscription, so the screen re-renders from it
      // instead of showing the old renewal date for one round trip. The list
      // behind it is invalidated because the row there says the same thing.
      queryClient.setQueryData(portalKeys.subscription(id), fresh)
      void queryClient.invalidateQueries({queryKey: portalKeys.subscriptions})
      setConfirming(false)
      toast.success(t("portal.subscription.cancelled"))
    },
    onError: error => toast.error(messageFor(error)),
  })

  useDocumentTitle(query.data?.description ?? null)

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

  const sub = query.data
  const invoices = sub.recent_invoices ?? []

  return (
    <div className="space-y-10">
      {/* The plan's name is the heading here, unlike the invoice screen. This
          page is about a thing that recurs, not about an amount: the recurring
          price is one of several facts and putting it in hero type would say
          the subscription is the money rather than the service. */}
      <header className="space-y-3">
        <BackLink/>
        <h1 className="text-xl font-semibold tracking-[-0.01em] text-foreground">
          {sub.description}
        </h1>
        <StatusBadge state={sub.state} tone={sub.tone}/>
      </header>

      <section aria-labelledby="plano" className="space-y-6">
        <h2 id="plano" className="sr-only">{t("portal.subscription.plan")}</h2>
        {/* Stacked on a phone, opposed on a desktop. A justified row with a
            long value on the right is how "Calculado quando o período fecha"
            ran off a 390px screen. */}
        <div className="space-y-1">
          <div className="flex flex-col gap-1 sm:flex-row sm:items-baseline sm:justify-between sm:gap-6">
            <span className="text-sm text-muted-foreground">{t("portal.subscription.perPeriod")}</span>
            {sub.metered ? (
              <span className="text-2xl font-semibold tracking-[-0.015em] text-foreground">
                {t("portal.subscription.metered")}
              </span>
            ) : (
              sub.amount != null && (
                <Money cents={sub.amount} currency={sub.currency} size="figure"/>
              )
            )}
          </div>
          {sub.metered && (
            <p className="text-sm text-muted-foreground sm:text-right">
              {t("portal.subscription.meteredNote")}
            </p>
          )}
        </div>

        {/* A description list on a rule, not a grid of boxes: these are
            labelled facts about one thing, and bordered cards would say they
            are several. */}
        <dl className="grid gap-x-6 gap-y-4 border-t border-border pt-6 sm:grid-cols-2">
          <Fact
            label={t("portal.subscription.currentPeriod")}
            value={period(sub.current_period.start, sub.current_period.end)}
          />
          <Fact
            label={sub.renews_on ? t("portal.subscription.nextCharge") : t("portal.subscription.accessUntil")}
            value={longDate(sub.renews_on ?? sub.current_period.end)}
          />
          {sub.since && <Fact label={t("portal.subscription.since")} value={longDate(sub.since)}/>}
          {!sub.renews_on && sub.cancelable && (
            <Fact label={t("portal.subscription.renewal")} value={t("portal.subscription.renewalCancelled")}/>
          )}
        </dl>
      </section>

      <section aria-labelledby="historico" className="space-y-4">
        <h2 id="historico" className="text-sm font-medium text-muted-foreground">
          {t("portal.subscription.invoices")}
        </h2>
        {invoices.length === 0 ? (
          // Not an illustrated empty state: this block is one section of a
          // screen that is working fine, and a panel with an icon would make a
          // plan on its first month look broken.
          <p className="text-sm text-muted-foreground">
            {t("portal.subscription.noInvoices")}
          </p>
        ) : (
          <ul className="-mx-3 divide-y divide-border">
            {invoices.map(invoice => (
              <InvoiceRow key={invoice.id} invoice={invoice}/>
            ))}
          </ul>
        )}
        {invoices.length > 0 && (
          <Link
            href="/invoices"
            className="inline-block text-sm text-brand-600 underline-offset-4 hover:underline"
          >
            {t("portal.subscription.seeInvoices")}
          </Link>
        )}
      </section>

      {/* Last, and plainly. A subscription whose exit a person cannot find is
          one they cancel by disputing the charge with their bank. */}
      {sub.cancelable && sub.renews_on && (
        <section className="border-t border-border pt-6">
          <Button variant="outline" onClick={() => setConfirming(true)}>
            {t("portal.subscription.cancel")}
          </Button>
        </section>
      )}

      <Modal
        open={confirming}
        onClose={() => setConfirming(false)}
        title={t("portal.subscription.cancelTitle")}
        description={t("portal.subscription.cancelDescription", {name: sub.description, date: longDate(sub.current_period.end)})}
        cancelLabel={t("portal.subscription.keep")}
        submitLabel={t("portal.subscription.cancel")}
        danger
        loading={cancel.isPending}
        onSubmit={() => cancel.mutate()}
      />
    </div>
  )
}

function Fact({label, value}: { label: string; value: string }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm text-foreground">{value}</dd>
    </div>
  )
}

function NotFound() {
  const {t} = useTranslation()
  return (
    <div className="space-y-6">
      <BackLink/>
      <div className="space-y-2">
        <h1 className="text-xl font-semibold tracking-[-0.01em] text-foreground">
          {t("portal.subscription.notFound.title")}
        </h1>
        <p className="text-sm text-muted-foreground">
          {t("portal.subscription.notFound.description")}
        </p>
      </div>
      <Button variant="outline" render={<Link href="/subscriptions"/>}>
        {t("portal.subscription.notFound.action")}
      </Button>
    </div>
  )
}

function BackLink() {
  const {t} = useTranslation()
  return (
    <Link
      href="/subscriptions"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      {t("portal.subscription.back")}
    </Link>
  )
}

function DetailSkeleton() {
  return (
    <div className="space-y-10" aria-busy>
      <div className="space-y-3">
        <Skeleton className="h-4 w-28"/>
        <Skeleton className="h-6 w-56"/>
        <Skeleton className="h-5 w-24 rounded-full"/>
      </div>
      <div className="space-y-4">
        <Skeleton className="h-8 w-40"/>
        <Skeleton className="h-4 w-full"/>
        <Skeleton className="h-4 w-4/6"/>
      </div>
      <div className="space-y-3">
        <Skeleton className="h-4 w-40"/>
        <Skeleton className="h-12 w-full"/>
        <Skeleton className="h-12 w-full"/>
      </div>
    </div>
  )
}
