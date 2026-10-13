"use client"

import {Badge, Button, Modal, PageHeader, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor} from "@/lib/api/client"
import {changeBillingPlan, consoleKeys, getBillingPlan} from "@/lib/api/console"
import type {BillingPlanOption, BillingPlanState} from "@/lib/api/consoleTypes"
import {useConsoleScope} from "@/lib/console/scope"
import {money} from "@/lib/format"

/** Desktop order is the catalogue's (cheapest first); literal classes so
 *  Tailwind keeps them. */
const LG_ORDER = ["lg:order-1", "lg:order-2", "lg:order-3", "lg:order-4", "lg:order-5", "lg:order-6"]

/**
 * Console → Plano (BYOK part C): this month's use first, then the public
 * plans. On a phone the cards stack with the current plan first; from lg up
 * they sit in a row in catalogue order. The current plan is marked by a ring
 * and a "Plano atual" badge (shape and word, never colour alone). A downgrade
 * below this month's use is allowed and the confirmation says what it means.
 */
export function BillingPlanView() {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const {data, isPending, error, refetch} = useQuery({queryKey: consoleKeys.plan(scope), queryFn: () => getBillingPlan(scope)})
  if (isPending) return <PlanSkeleton/>
  if (error || !data) return <ErrorBlock error={error} onRetry={() => void refetch()}/>
  return (
    <div className="space-y-8">
      <PageHeader title={t("console.plan.title")} description={t("console.plan.description")}/>
      <PlanScreen state={data} live={scope.mode === "live"}/>
    </div>
  )
}

function PlanScreen({state, live}: {state: BillingPlanState; live: boolean}) {
  const {t} = useTranslation()
  const scope = useConsoleScope()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<BillingPlanOption | null>(null)
  const name = (plan: string) => t(`console.plan.names.${plan}`, {defaultValue: plan})
  const listed = state.plans.some(p => p.plan === state.current.plan)
  // The current plan first in the DOM (the phone's stacked list); lg:order-*
  // restores the catalogue order in the row.
  const cards = state.plans.map((p, i) => ({p, i}))
    .sort((a, b) => Number(b.p.plan === state.current.plan) - Number(a.p.plan === state.current.plan))

  const change = useMutation({
    mutationFn: (plan: string) => changeBillingPlan(scope, plan),
    onSuccess: next => {
      queryClient.setQueryData(consoleKeys.plan(scope), next)
      toast.success(t("console.plan.changed", {plan: name(next.current.plan)}))
    },
    onError: (err: unknown) => toast.error(messageFor(err)),
    onSettled: () => setTarget(null),
  })

  const below = target && target.included >= 0 && state.usage.charges_this_month > target.included
  return (
    <>
      {!live && <p className="-mt-6 text-sm text-muted-foreground">{t("console.plan.liveOnly")}</p>}
      <Usage state={state}/>
      {!listed && <p className="text-sm text-muted-foreground">{t("console.plan.currentOther", {plan: name(state.current.plan)})}</p>}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map(({p, i}) => {
          const isCurrent = p.plan === state.current.plan
          return (
            <article
              key={p.plan}
              aria-current={isCurrent ? "true" : undefined}
              className={`flex flex-col gap-4 rounded-lg border p-4 ${LG_ORDER[i] ?? ""} ${isCurrent ? "border-brand-600 ring-1 ring-brand-600" : "border-border"}`}
            >
              <div className="flex items-start justify-between gap-2">
                <h3 className="text-base font-semibold">{name(p.plan)}</h3>
                {isCurrent && <Badge tone="neutral">{t("console.plan.current")}</Badge>}
              </div>
              <PlanFacts option={p}/>
              {live && !isCurrent && (
                <div className="mt-auto pt-2">
                  <Button variant="outline" className="w-full" onClick={() => setTarget(p)} disabled={change.isPending}>
                    {t("console.plan.changeTo", {plan: name(p.plan)})}
                  </Button>
                </div>
              )}
            </article>
          )
        })}
      </div>

      <Modal
        open={target !== null}
        onClose={() => setTarget(null)}
        title={target ? t("console.plan.confirm.title", {plan: name(target.plan)}) : ""}
        submitLabel={target ? t("console.plan.changeTo", {plan: name(target.plan)}) : ""}
        loading={change.isPending}
        onSubmit={() => target && change.mutate(target.plan)}
      >
        {target && (
          <div className="space-y-2 text-sm">
            <p>{target.fee > 0 ? t("console.plan.confirm.paid", {amount: money(target.fee)}) : t("console.plan.confirm.free")}</p>
            {below && (
              <p>{t("console.plan.confirm.below", {used: state.usage.charges_this_month, plan: name(target.plan), quota: target.included})}</p>
            )}
          </div>
        )}
      </Modal>
    </>
  )
}

/** Price, what is included and what happens past it, then the features. */
function PlanFacts({option}: {option: BillingPlanOption}) {
  const {t} = useTranslation()
  return (
    <div className="space-y-2">
      <p className="text-xl font-semibold tabular-nums">
        {option.fee === 0 ? t("console.plan.free") : t("console.plan.perMonth", {amount: money(option.fee)})}
      </p>
      <ul className="space-y-0.5 text-sm text-muted-foreground">
        <li>{t("console.plan.included", {count: option.included})}</li>
        <li>{option.refuse ? t("console.plan.refuse") : t("console.plan.overage", {amount: money(option.overage_unit)})}</li>
        {option.bank_connections >= 0 && <li>{t("console.plan.connections", {count: option.bank_connections})}</li>}
        {option.users >= 0 && <li>{t("console.plan.users", {count: option.users})}</li>}
        {option.api_access && <li>{t("console.plan.apiAccess")}</li>}
      </ul>
    </div>
  )
}

/** This month's use against the plan: plain figures, no colour beyond text. */
function Usage({state}: {state: BillingPlanState}) {
  const {t} = useTranslation()
  const u = state.usage
  const line = (key: string, used: number, quota: number) =>
    quota < 0 ? t(`console.plan.usage.${key}Unlimited`, {used}) : t(`console.plan.usage.${key}`, {used, quota})
  return (
    <section aria-labelledby="plan-usage" className="space-y-2">
      <h2 id="plan-usage" className="text-sm font-medium text-muted-foreground">{t("console.plan.usage.title")}</h2>
      <p className="text-lg font-semibold tabular-nums">{line("charges", u.charges_this_month, u.quota)}</p>
      <ul className="space-y-0.5 text-sm tabular-nums text-muted-foreground">
        <li>{line("subscriptions", u.subscriptions_active, u.subscriptions_quota)}</li>
        <li>{line("connections", u.bank_connections, u.bank_connections_quota)}</li>
      </ul>
    </section>
  )
}

function PlanSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-7 w-32"/>
      <Skeleton className="h-6 w-48"/>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[0, 1, 2, 3].map(i => <Skeleton key={i} className="h-44 w-full"/>)}
      </div>
    </div>
  )
}
