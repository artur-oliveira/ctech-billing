"use client"

import {Button, Skeleton} from "@aoctech/ui"
import {Modal} from "@/components/ui/ConsoleOverlay"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ArrowLeft} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {SubscriptionStatusBadge} from "@/components/console/SubscriptionStatusBadge"
import {Timeline} from "@/components/console/Timeline"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor, statusOf} from "@/lib/api/client"
import {cancelConsoleSubscription, consoleKeys, getConsoleSubscription} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"
import {longDate, money} from "@/lib/format"

/**
 * C5 — one subscription.
 *
 * Cancelling is **two buttons, never a checkbox inside one modal**. Ending a
 * subscription now and letting the paid period run out are different decisions
 * with different consequences — the first stops entitlement immediately, the
 * second keeps it until a period the customer has already paid for — and
 * hiding that behind a toggle is how an operator cuts somebody's access on the
 * day they paid.
 */
export default function ConsoleSubscriptionPage() {
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
  const queryClient = useQueryClient()
  const [ending, setEnding] = useState<"now" | "period-end" | null>(null)

  const query = useQuery({
    queryKey: consoleKeys.subscription(mode, id),
    queryFn: () => getConsoleSubscription(id, mode),
    enabled: id !== "",
  })

  const cancel = useMutation({
    mutationFn: (atPeriodEnd: boolean) => cancelConsoleSubscription(id, atPeriodEnd, mode),
    onSuccess: () => {
      toast.success(t("console.subscription.done"))
      void queryClient.invalidateQueries({queryKey: consoleKeys.subscription(mode, id)})
      void queryClient.invalidateQueries({queryKey: consoleKeys.subscriptions(mode)})
    },
    onError: error => toast.error(messageFor(error)),
    onSettled: () => setEnding(null),
  })

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

  const {subscription: sub, items, timeline} = query.data
  const live = sub.status !== "CANCELED"

  return (
    <div className="space-y-8">
      <header className="space-y-3">
        <BackLink/>
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-2">
            <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{sub.id}</h1>
            <SubscriptionStatusBadge
              status={sub.status}
              endingAtPeriodEnd={sub.cancel_at_period_end}
            />
          </div>
          {live && (
            <div className="flex flex-wrap gap-2">
              {!sub.cancel_at_period_end && (
                <Button variant="outline" size="sm" onClick={() => setEnding("period-end")}>
                  {t("console.subscription.endAtPeriodEnd")}
                </Button>
              )}
              <Button variant="outline" size="sm" onClick={() => setEnding("now")}>
                {t("console.subscription.endNow")}
              </Button>
            </div>
          )}
        </div>
      </header>

      <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 text-sm sm:grid-cols-3">
        <Fact
          label={t("console.subscription.customer")}
          value={sub.customer_id}
          href={`/console/customer?id=${sub.customer_id}`}
        />
        <Fact label={t("console.subscription.period")} value={t("common.periodRange", {start: longDate(sub.current_period.start), end: longDate(sub.current_period.end)})}/>
        <Fact label={t("console.subscription.anchor")} value={longDate(sub.anchor)}/>
        <Fact label={t("console.subscription.access")} value={sub.entitled ? t("console.subscription.allowed") : t("console.subscription.blocked")}/>
        <Fact
          label={t("console.subscription.billing")}
          value={sub.billing_timing === "arrears" ? t("console.timing.arrears") : t("console.timing.advance")}
        />
      </dl>

      <section aria-labelledby="itens" className="space-y-3">
        <h2 id="itens" className="text-sm font-medium text-muted-foreground">{t("console.subscription.items")}</h2>
        <ul className="divide-y divide-border border-y border-border">
          {items.map(item => (
            <li key={item.id} className="flex flex-wrap items-baseline justify-between gap-3 py-2">
              <div className="min-w-0">
                <p className="text-sm text-foreground">{item.price_id}</p>
                <p className="text-xs text-muted-foreground">
                  {item.price.type === "metered" ? t("console.priceType.metered") : t("console.priceType.fixed")} · {t("console.subscription.quantity", {count: item.quantity})}
                  {item.price.archived && ` · ${t("console.subscription.archivedPrice")}`}
                </p>
              </div>
              <span data-numeric className="text-sm text-foreground">
                {money(item.price.unit_amount, item.price.currency)}
                {item.price.type === "metered" && (
                  <span className="text-muted-foreground"> {t("console.priceType.perUnit")}</span>
                )}
              </span>
            </li>
          ))}
        </ul>
      </section>

      <Timeline entries={timeline}/>

      <Modal
        open={ending !== null}
        onClose={() => setEnding(null)}
        title={ending === "now" ? t("console.subscription.nowTitle") : t("console.subscription.endTitle")}
        description={
          ending === "now"
            ? t("console.subscription.nowBody")
            : t("console.subscription.endBody", {date: longDate(sub.current_period.end)})
        }
        cancelLabel={t("console.subscription.keep")}
        submitLabel={ending === "now" ? t("console.subscription.endNow") : t("console.subscription.endAtPeriodEnd")}
        danger={ending === "now"}
        loading={cancel.isPending}
        onSubmit={() => cancel.mutate(ending === "period-end")}
      />
    </div>
  )
}

function Fact({label, value, href}: { label: string; value: string; href?: string }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate text-foreground">
        {href ? (
          <Link href={href} className="text-brand-600 underline-offset-4 hover:underline">
            {value}
          </Link>
        ) : (
          value
        )}
      </dd>
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
          {t("console.subscription.notFound")}
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
      href="/console/subscriptions"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      {t("console.subscription.back")}
    </Link>
  )
}

function DetailSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-6 w-56"/>
      <Skeleton className="h-16 w-full"/>
      <Skeleton className="h-24 w-full"/>
    </div>
  )
}
