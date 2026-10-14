"use client";

import {Alert, Badge, Button, EmptyState, Field, Input, Modal, PageHeader, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {useRouter} from "next/navigation";
import {UserRound} from "lucide-react";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {messageFor} from "@/lib/api/client";
import {choosePlan, cancelPlan, financeKeys, getPlan, isPlanPaymentPending} from "@/lib/api/finance";
import type {PlanState} from "@/lib/api/financeTypes";
import {useAuth} from "@/lib/auth/AuthContext";
import {PERSONAL, setSpace} from "@/lib/console/space";
import {useSpace} from "@/lib/console/useSpace";
import {currentOption, overLimits, pendingOption, type PlanOption, planOptions, suggestBasic} from "@/lib/finance/plan";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {money, shortDate} from "@/lib/format";

type Confirm =
  | {kind: "free"}
  | {kind: "schedule"; option: PlanOption}
  | {kind: "pending"; checkoutURL?: string}
  | {kind: "name"; option: PlanOption}

/**
 * Finanças → Plano (plans spec § 7): the person's own plan, from Pessoal.
 *
 * Usage first — it is what the person came to check, and on a phone it is the
 * only thing above the fold — then the plans as cards, the current one marked
 * by a ring and a "Seu plano" badge (shape and word, never colour alone).
 * Nothing on this screen removes anything: over the limit is a banner that says
 * so, and the way back is the person's.
 */
export function PlanView() {
  const {t} = useTranslation();
  const space = useSpace();
  const ctx = useFinanceCtx();
  const personal = space.kind === "personal";
  const {data: state, isPending, error, refetch} = useQuery({
    queryKey: financeKeys.plan(ctx.mode, ctx.space),
    queryFn: () => getPlan(ctx),
    enabled: personal,
  });

  if (!personal) {
    return (
      <EmptyState
        icon={<UserRound/>}
        title={t("finance.plan.outside.title")}
        description={t("finance.plan.outside.description")}
        action={<Button onClick={() => setSpace(PERSONAL)}>{t("finance.plan.outside.action")}</Button>}
      />
    );
  }
  if (isPending) return <PlanSkeleton/>;
  if (error || !state) return <ErrorBlock error={error} onRetry={() => void refetch()}/>;
  return <PlanScreen state={state}/>;
}

function PlanScreen({state}: {state: PlanState}) {
  const {t} = useTranslation();
  const {name: authName} = useAuth();
  const router = useRouter();
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [invoiceName, setInvoiceName] = useState("");

  const options = planOptions(state);
  const current = currentOption(state, options);
  const over = overLimits(state, current);
  const pending = pendingOption(state, options);
  const suggestion = suggestBasic(state, options, current);
  const periodEnd = state.subscription ? shortDate(state.subscription.period.period_end) : "";
  const label = (o: PlanOption) => t(`finance.plan.names.${o.key}`, {defaultValue: o.key});

  const choose = useFinanceMutation(
    (c, vars: {option: PlanOption; name: string}, key) => choosePlan(c, {price_ids: vars.option.priceIds, name: vars.name}, key),
    c => [financeKeys.plan(c.mode, c.space)],
    (result, vars) => {
      if (result.invoice?.checkout_url) {
        window.location.assign(result.invoice.checkout_url);
        return;
      }
      if (result.invoice) {
        toast.success(t("finance.plan.toast.invoiceNoLink"), {
          action: {label: t("finance.plan.toast.openInvoice"), onClick: () => router.push(`/invoice?id=${result.invoice!.id}`)},
        });
        return;
      }
      const scheduled = result.subscription?.pending_change;
      if (scheduled && vars.option.priceIds.every(id => scheduled.price_ids.includes(id))) {
        toast.success(t("finance.plan.toast.scheduled", {date: shortDate(scheduled.at)}));
        return;
      }
      toast.success(t("finance.plan.toast.changed"));
    },
    err => {
      const pay = isPlanPaymentPending(err);
      if (pay) setConfirm({kind: "pending", checkoutURL: pay.checkout_url});
      else toast.error(messageFor(err));
    },
  );
  const cancel = useFinanceMutation(
    (c, _: void, key) => cancelPlan(c, key),
    c => [financeKeys.plan(c.mode, c.space)],
    // An unpaid first plan is cancelled at once; a paid one runs to its end.
    () => toast.success(state.subscription?.status === "INCOMPLETE"
      ? t("finance.plan.toast.changed")
      : t("finance.plan.toast.freeScheduled", {date: periodEnd})),
    err => toast.error(messageFor(err)),
  );

  const submit = (option: PlanOption, name = authName ?? "") => {
    if (!name.trim()) {
      setConfirm({kind: "name", option});
      return;
    }
    choose.mutate({option, name: name.trim()});
  };

  // What a card's button does. Free is never subscribed to (D9): from a paid
  // plan it is "back to Free" at the period end. Advance → Sob demanda waits
  // for the paid period to end, so it asks first (plans spec, amendment).
  const act = (option: PlanOption) => {
    if (option.isDefault) {
      setConfirm({kind: "free"});
      return;
    }
    const fromAdvance = !current.isDefault && current.monthly !== null;
    if (fromAdvance && option.monthly === null && state.subscription?.status === "ACTIVE") {
      setConfirm({kind: "schedule", option});
      return;
    }
    submit(option);
  };

  return (
    <div className="space-y-8">
      <PageHeader title={t("finance.plan.title")} description={t("finance.plan.description")}/>
      {state.live_only && <p className="-mt-6 text-sm text-muted-foreground">{t("finance.plan.liveOnly")}</p>}

      <Usage state={state} current={current}/>

      {over.length > 0 && (
        <Alert tone="warning" title={t("finance.plan.over.title")}>
          <ul className="space-y-1">
            {over.map(o => (
              <li key={`${o.resource}-${o.spaceName ?? ""}`}>
                {o.resource === "spaces"
                  ? t("finance.plan.over.spaces", {used: o.used, limit: o.limit, plan: label(current)})
                  : t("finance.plan.over.people", {space: o.spaceName, used: o.used, limit: o.limit, plan: label(current)})}
              </li>
            ))}
          </ul>
        </Alert>
      )}

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {options.map((option, i) => {
          const isCurrent = option === current;
          // One primary action (spec § 7): paying an open first invoice when
          // there is one; otherwise the next priced plan up — an upgrade, never
          // a scheduled move to Sob demanda.
          const isNext = !state.open_invoice?.checkout_url && i === options.indexOf(current) + 1 &&
            !option.isDefault && option.monthly !== null && option.monthly > (current.monthly ?? 0);
          const isPendingTarget = pending?.option === option;
          return (
            <article
              key={option.key}
              aria-current={isCurrent ? "true" : undefined}
              className={`flex flex-col gap-4 rounded-lg border p-4 ${isCurrent ? "border-brand-600 ring-1 ring-brand-600" : "border-border"}`}
            >
              <div className="flex items-start justify-between gap-2">
                <h3 className="text-base font-semibold">{label(option)}</h3>
                {isCurrent && <Badge tone="neutral">{t("finance.plan.current")}</Badge>}
              </div>

              <PlanFacts option={option}/>

              {isCurrent && state.subscription?.cancel_at_period_end && (
                <p className="text-sm text-muted-foreground">{t("finance.plan.endsOn", {date: periodEnd})}</p>
              )}
              {isCurrent && pending && !state.subscription?.cancel_at_period_end && (
                <p className="text-sm text-muted-foreground">{t("finance.plan.changesOn", {plan: label(pending.option), date: shortDate(pending.at)})}</p>
              )}
              {isCurrent && option.monthly === null && !option.isDefault && (
                <p className="text-sm tabular-nums">{t("finance.plan.projected", {amount: money(state.projected_ondemand_cents)})}</p>
              )}
              {isCurrent && suggestion && (
                <div className="space-y-2 border-t border-border pt-3">
                  <p className="text-sm">{t("finance.plan.suggest", {projected: money(state.projected_ondemand_cents), basic: money(suggestion.monthly ?? 0)})}</p>
                  <Button variant="outline" size="sm" onClick={() => act(suggestion)} disabled={choose.isPending}>
                    {t("finance.plan.suggestAction")}
                  </Button>
                </div>
              )}

              <div className="mt-auto pt-2">
                <CardAction
                  option={option}
                  isCurrent={isCurrent}
                  isPendingTarget={isPendingTarget}
                  pendingAt={pending?.at}
                  state={state}
                  current={current}
                  label={label}
                  primary={isNext}
                  busy={choose.isPending || cancel.isPending}
                  onAct={() => act(option)}
                  onKeep={() => submit(current)}
                />
              </div>
            </article>
          );
        })}
      </div>

      <Modal
        open={confirm?.kind === "free"}
        onClose={() => setConfirm(null)}
        title={t("finance.plan.confirmFree.title")}
        description={t("finance.plan.confirmFree.body", {date: periodEnd})}
        submitLabel={t("finance.plan.confirmFree.submit")}
        loading={cancel.isPending}
        onSubmit={() => cancel.mutate(undefined, {onSettled: () => setConfirm(null)})}
      />
      <Modal
        open={confirm?.kind === "schedule"}
        onClose={() => setConfirm(null)}
        title={t("finance.plan.confirmSchedule.title")}
        description={t("finance.plan.confirmSchedule.body", {plan: label(current), date: periodEnd})}
        submitLabel={t("finance.plan.confirmSchedule.submit")}
        loading={choose.isPending}
        onSubmit={() => {
          if (confirm?.kind === "schedule") submit(confirm.option);
          setConfirm(null);
        }}
      />
      <Modal
        open={confirm?.kind === "pending"}
        onClose={() => setConfirm(null)}
        title={t("finance.plan.pending.title")}
        description={t("finance.plan.pending.body")}
        submitLabel={t("finance.plan.pending.submit")}
        submitDisabled={confirm?.kind === "pending" && !confirm.checkoutURL}
        onSubmit={() => {
          if (confirm?.kind === "pending" && confirm.checkoutURL) window.location.assign(confirm.checkoutURL);
        }}
      />
      <Modal
        open={confirm?.kind === "name"}
        onClose={() => setConfirm(null)}
        title={t("finance.plan.name.title")}
        submitLabel={t("finance.plan.name.submit")}
        submitDisabled={!invoiceName.trim()}
        onSubmit={() => {
          if (confirm?.kind === "name") submit(confirm.option, invoiceName);
          setConfirm(null);
        }}
      >
        <Field label={t("finance.plan.name.label")} htmlFor="plan-invoice-name">
          <Input id="plan-invoice-name" value={invoiceName} onChange={e => setInvoiceName(e.target.value)} autoComplete="name"/>
        </Field>
      </Modal>
    </div>
  );
}

/** The plan's price and what it allows, in the order a person compares them. */
function PlanFacts({option}: {option: PlanOption}) {
  const {t} = useTranslation();
  const meter = (m: string) => option.metered.find(x => x.meter === m);
  const spaces = meter("finance_spaces"), people = meter("finance_people");
  return (
    <div className="space-y-2">
      {option.monthly !== null ? (
        <p className="text-xl font-semibold tabular-nums">
          {option.monthly === 0 ? t("finance.plan.free") : t("finance.plan.perMonth", {amount: money(option.monthly)})}
        </p>
      ) : (
        <div className="space-y-0.5 text-sm tabular-nums">
          {spaces && <p className="font-medium">{t("finance.plan.meteredSpaces", {amount: money(spaces.unitAmount)})}</p>}
          {people && <p className="font-medium">{t("finance.plan.meteredPeople", {amount: money(people.unitAmount)})}</p>}
          <p className="text-muted-foreground">{t("finance.plan.meteredNote")}</p>
        </div>
      )}
      <ul className="space-y-0.5 text-sm text-muted-foreground">
        <li>{option.quotaSpaces === Infinity ? t("finance.plan.spacesUnlimited") : t("finance.plan.spaces", {count: option.quotaSpaces})}</li>
        <li>{option.quotaPeople === Infinity ? t("finance.plan.peopleUnlimited") : t("finance.plan.people", {count: option.quotaPeople})}</li>
      </ul>
    </div>
  );
}

function CardAction({option, isCurrent, isPendingTarget, pendingAt, state, current, label, primary, busy, onAct, onKeep}: {
  option: PlanOption; isCurrent: boolean; isPendingTarget: boolean; pendingAt?: string; state: PlanState
  current: PlanOption; label: (o: PlanOption) => string; primary: boolean; busy: boolean; onAct: () => void; onKeep: () => void
}) {
  const {t} = useTranslation();
  if (isCurrent) {
    const link = state.open_invoice?.checkout_url;
    return link ? <Button className="w-full" onClick={() => window.location.assign(link)}>{t("finance.plan.pay")}</Button> : null;
  }
  if (isPendingTarget && pendingAt) {
    return (
      <div className="space-y-2">
        <p className="text-sm text-muted-foreground">{t("finance.plan.startsOn", {date: shortDate(pendingAt)})}</p>
        <Button variant="outline" className="w-full" onClick={onKeep} disabled={busy}>{t("finance.plan.keep", {plan: label(current)})}</Button>
      </div>
    );
  }
  if (option.isDefault) {
    if (state.subscription?.cancel_at_period_end) return null;
    return <Button variant="outline" className="w-full" onClick={onAct} disabled={busy}>{t("finance.plan.backToFree")}</Button>;
  }
  return (
    <Button variant={primary ? "brand" : "outline"} className="w-full" onClick={onAct} disabled={busy}>
      {current.isDefault ? t("finance.plan.subscribe") : t("finance.plan.changeTo", {plan: label(option)})}
    </Button>
  );
}

/** Usage: spaces against the plan, then each owned space's people. */
function Usage({state, current}: {state: PlanState; current: PlanOption}) {
  const {t} = useTranslation();
  if (state.usage.unavailable) {
    return <p className="text-sm text-muted-foreground">{t("finance.plan.usage.unavailable")}</p>;
  }
  const spaces = state.usage.spaces;
  return (
    <section aria-labelledby="plan-usage" className="space-y-2">
      <h2 id="plan-usage" className="text-sm font-medium text-muted-foreground">{t("finance.plan.usage.title")}</h2>
      <p className="text-lg font-semibold tabular-nums">
        {current.quotaSpaces === Infinity
          ? t("finance.plan.usage.spacesUnlimited", {count: spaces.length})
          : t("finance.plan.usage.spaces", {used: spaces.length, limit: current.quotaSpaces})}
      </p>
      {spaces.length > 0 && (
        <ul className="divide-y divide-border border-y border-border text-sm">
          {spaces.map(s => {
            const used = s.people + s.pending_invitations;
            return (
              <li key={s.id} className="flex flex-wrap items-baseline justify-between gap-x-4 py-2 tabular-nums">
                <span>
                  {current.quotaPeople === Infinity
                    ? t("finance.plan.usage.spaceUnlimited", {name: s.display_name, count: used})
                    : t("finance.plan.usage.space", {name: s.display_name, used, limit: current.quotaPeople})}
                </span>
                {s.pending_invitations > 0 && (
                  <span className="text-muted-foreground">{t("finance.plan.usage.pending", {count: s.pending_invitations})}</span>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function PlanSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-7 w-32"/>
      <Skeleton className="h-6 w-48"/>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {[0, 1, 2, 3].map(i => <Skeleton key={i} className="h-44 w-full"/>)}
      </div>
    </div>
  );
}
