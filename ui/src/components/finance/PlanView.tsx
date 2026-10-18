"use client";

import {Alert, Button, EmptyState, Field, Input, Modal, PageHeader, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {useRouter} from "next/navigation";
import {UserRound} from "lucide-react";
import {useEffect, useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {FormError, FormErrorText} from "@/components/finance/FormError";
import {InlineConfirm} from "@/components/finance/InlineConfirm";
import {PLAN_STATUS, StatusBadge} from "@/components/finance/StatusBadge";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount, AmountRow} from "@/components/ui/Amount";
import {Section} from "@/components/ui/Section";
import {choosePlan, cancelPlan, financeKeys, getPlan, isPlanPaymentPending} from "@/lib/api/finance";
import type {PlanState} from "@/lib/api/financeTypes";
import {useAuth} from "@/lib/auth/AuthContext";
import {PERSONAL, setSpace} from "@/lib/console/space";
import {useSpace} from "@/lib/console/useSpace";
import {currentOption, overLimits, pendingOption, type PlanOption, planOptions, planStatus, suggestBasic} from "@/lib/finance/plan";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {money, shortDate} from "@/lib/format";
import {dueLabel, todayIso} from "@/lib/finance/today";

type Confirm =
  | {kind: "free"}
  | {kind: "schedule"; option: PlanOption}
  | {kind: "change"; option: PlanOption}
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
  // The 409's own checkout link, for when the plan state has not caught up with it yet.
  const [blockedURL, setBlockedURL] = useState<string | undefined>();
  // Between "go pay" and the page leaving: one press only.
  const [leaving, setLeaving] = useState(false);

  const options = planOptions(state);
  const current = currentOption(state, options);
  const over = overLimits(state, current);
  const pending = pendingOption(state, options);
  const suggestion = suggestBasic(state, options, current);
  const periodEnd = state.subscription ? shortDate(state.subscription.period.period_end) : "";
  const label = (o: PlanOption) => t(`finance.plan.names.${o.key}`, {defaultValue: t("finance.plan.names.other")});
  const payURL = state.open_invoice?.checkout_url ?? blockedURL;
  const owing = !!state.open_invoice || !!blockedURL;

  const goPay = (url: string) => {
    setLeaving(true);
    window.location.assign(url);
  };

  const choose = useFinanceMutation(
    (c, vars: {option: PlanOption; name: string}, key) => choosePlan(c, {price_ids: vars.option.priceIds, name: vars.name}, key),
    c => [financeKeys.plan(c.mode, c.space)],
    (result, vars) => {
      setConfirm(null);
      if (result.invoice?.checkout_url) {
        goPay(result.invoice.checkout_url);
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
      if (pay) {
        setConfirm(null);
        setBlockedURL(pay.checkout_url ?? "");
      }
    },
    {invalidateOnError: true},
  );
  const cancel = useFinanceMutation(
    (c, _: void, key) => cancelPlan(c, key),
    c => [financeKeys.plan(c.mode, c.space)],
    // An unpaid first plan is cancelled at once; a paid one runs to its end.
    () => {
      setConfirm(null);
      toast.success(state.subscription?.status === "INCOMPLETE"
        ? t("finance.plan.toast.changed")
        : t("finance.plan.toast.freeScheduled", {date: periodEnd}));
    },
  );

  // A confirmation opens inside its own card; the suggestion's button lives
  // elsewhere, so bring that card into view.
  const confirmKey = confirm && confirm.kind !== "name" ? (confirm.kind === "free" ? "free" : confirm.option.key) : null;
  useEffect(() => {
    if (confirmKey) document.getElementById(`plan-card-${confirmKey}`)?.scrollIntoView({block: "nearest"});
  }, [confirmKey]);

  const submit = (option: PlanOption, name = authName ?? "") => {
    if (!name.trim()) {
      setConfirm({kind: "name", option});
      return;
    }
    choose.mutate({option, name: name.trim()});
  };

  const ask = (next: Confirm) => {
    choose.reset();
    cancel.reset();
    setConfirm(next);
  };
  const dismiss = () => setConfirm(null);

  // What a card's button does. Free is never subscribed to (D9): from a paid
  // plan it is "back to Free" at the period end. Every paid move says what it
  // will do first; Advance → Sob demanda waits for the paid period to end.
  const act = (option: PlanOption) => {
    if (option.isDefault) {
      ask({kind: "free"});
      return;
    }
    const fromAdvance = !current.isDefault && current.monthly !== null;
    if (fromAdvance && option.monthly === null && state.subscription?.status === "ACTIVE") {
      ask({kind: "schedule", option});
      return;
    }
    ask({kind: "change", option});
  };

  const busy = choose.isPending || cancel.isPending || leaving;

  return (
    <div className="space-y-8">
      <PageHeader title={t("finance.plan.title")} description={t("finance.plan.description")}/>
      {state.live_only && <p className="-mt-6 text-sm text-muted-foreground">{t("finance.plan.liveOnly")}</p>}

      {owing && (
        <Alert tone="warning" title={t("finance.plan.owing.title")}>
          <div className="space-y-3">
            <p>
              {state.open_invoice
                ? t("finance.plan.owing.body", {amount: money(state.open_invoice.total_cents), date: shortDate(state.open_invoice.due_date)})
                : t("finance.plan.owing.bodyBare")}
            </p>
            {payURL && (
              <Button variant="brand" disabled={leaving} onClick={() => goPay(payURL)}>
                {state.open_invoice ? t("finance.plan.owing.payAmount", {amount: money(state.open_invoice.total_cents)}) : t("finance.plan.pay")}
              </Button>
            )}
          </div>
        </Alert>
      )}

      <Summary state={state} current={current} pending={pending} suggestion={suggestion} label={label} busy={busy} onSuggest={() => suggestion && act(suggestion)}/>

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

      <Usage state={state} current={current}/>

      <Section title={t("finance.plan.all")} className="space-y-3">
        <FormError error={confirm ? undefined : choose.error ?? cancel.error}/>
        <div className="@container">
          <div className="grid gap-4 @lg:grid-cols-2 @5xl:grid-cols-4">
            {options.map((option, i) => {
              const isCurrent = option === current;
              // One primary action (spec § 7): the next priced plan up — an upgrade,
              // never a scheduled move to Sob demanda — and none while an invoice is owed.
              const isNext = !owing && i === options.indexOf(current) + 1 &&
                !option.isDefault && option.monthly !== null && option.monthly > (current.monthly ?? 0);
              const isPendingTarget = pending?.option === option;
              const asked = confirm && confirm.kind !== "name" && (confirm.kind === "free" ? option.isDefault : confirm.option.key === option.key) ? confirm : null;
              return (
                <article
                  key={option.key}
                  id={`plan-card-${option.key}`}
                  className={`flex min-w-0 flex-col gap-4 rounded-lg border p-4 ${isCurrent ? "border-brand-600 ring-1 ring-brand-600" : "border-border"}`}
                >
                  <div className="flex items-start justify-between gap-2">
                    <h3 className="text-base font-semibold">{label(option)}</h3>
                    {isCurrent && <StatusBadge status={PLAN_STATUS.current}>{t("finance.plan.current")}</StatusBadge>}
                  </div>

                  <PlanFacts option={option}/>

                  <div className="mt-auto space-y-2 pt-2">
                    {asked ? (
                      <ConfirmBox
                        confirm={asked}
                        current={current}
                        label={label}
                        periodEnd={periodEnd}
                        pending={choose.isPending || cancel.isPending}
                        error={asked.kind === "free" ? cancel.error : choose.error}
                        onCancel={dismiss}
                        onConfirm={() => asked.kind === "free" ? cancel.mutate() : submit(asked.option)}
                      />
                    ) : (
                      <CardAction
                        option={option}
                        isCurrent={isCurrent}
                        isPendingTarget={isPendingTarget}
                        pendingAt={pending?.at}
                        state={state}
                        current={current}
                        label={label}
                        primary={isNext}
                        owing={owing}
                        busy={busy}
                        onAct={() => act(option)}
                        onKeep={() => submit(current)}
                      />
                    )}
                  </div>
                </article>
              );
            })}
          </div>
        </div>
      </Section>

      <Modal
        open={confirm?.kind === "name"}
        onClose={dismiss}
        title={t("finance.plan.name.title")}
        submitLabel={t("finance.plan.name.submit")}
        submitDisabled={!invoiceName.trim()}
        onSubmit={() => {
          if (confirm?.kind === "name") submit(confirm.option, invoiceName);
        }}
      >
        <Field label={t("finance.plan.name.label")} htmlFor="plan-invoice-name">
          <Input id="plan-invoice-name" value={invoiceName} onChange={e => setInvoiceName(e.target.value)} autoComplete="name" required/>
        </Field>
      </Modal>
    </div>
  );
}

/**
 * What the person pays CTech, in the order they ask it: which plan, how much,
 * when it renews or ends. The cards below are the catalogue; this is the answer.
 */
function Summary({state, current, pending, suggestion, label, busy, onSuggest}: {
  state: PlanState; current: PlanOption; pending: ReturnType<typeof pendingOption>; suggestion: PlanOption | null
  label: (o: PlanOption) => string; busy: boolean; onSuggest: () => void
}) {
  const {t} = useTranslation();
  const sub = state.subscription;
  const status = planStatus(state);
  const end = sub?.period.period_end ?? "";
  // "vence em 3 dias" says a charge is coming; an end date is not a charge.
  const due = sub && !sub.cancel_at_period_end ? dueLabel(end.slice(0, 10), todayIso()) : null;
  const metered = current.monthly === null && !current.isDefault;
  return (
    <Section title={t("finance.plan.summary")} className="space-y-3">
      <dl className="divide-y divide-border border-y border-border text-sm">
        <AmountRow label={t("finance.plan.row.plan")}>
          <span className="flex flex-wrap items-center justify-end gap-2">
            <span className="font-medium">{label(current)}</span>
            {status && <StatusBadge status={PLAN_STATUS[status]}>{t(`finance.plan.status.${status}`)}</StatusBadge>}
          </span>
        </AmountRow>
        <AmountRow label={t("finance.plan.row.price")}>
          {metered
            ? <span className="text-muted-foreground">{t("finance.plan.row.metered")}</span>
            : <Amount strong>{current.isDefault || current.monthly === 0 ? t("finance.plan.free") : t("finance.plan.perMonth", {amount: money(current.monthly ?? 0)})}</Amount>}
        </AmountRow>
        {metered && (
          <AmountRow label={t("finance.plan.row.projected")}><Amount strong>{money(state.projected_ondemand_cents)}</Amount></AmountRow>
        )}
        {sub && (
          <AmountRow label={t(sub.cancel_at_period_end ? "finance.plan.row.endsOn" : "finance.plan.row.renewsOn")}>
            <span>{shortDate(end)}{due && <span className="text-muted-foreground"> · {due}</span>}</span>
          </AmountRow>
        )}
      </dl>
      {sub?.cancel_at_period_end && <p className="text-sm text-muted-foreground">{t("finance.plan.endsNote")}</p>}
      {pending && !sub?.cancel_at_period_end && (
        <p className="text-sm text-muted-foreground">{t("finance.plan.changesOn", {plan: label(pending.option), date: shortDate(pending.at)})}</p>
      )}
      {suggestion && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg bg-surface p-3 text-sm">
          <p className="min-w-0 flex-1 basis-56">{t("finance.plan.suggest", {projected: money(state.projected_ondemand_cents), basic: money(suggestion.monthly ?? 0)})}</p>
          <Button variant="outline" size="sm" onClick={onSuggest} disabled={busy}>{t("finance.plan.suggestAction")}</Button>
        </div>
      )}
    </Section>
  );
}

/** The question a card's button asks first: what will happen, then the yes. */
function ConfirmBox({confirm, current, label, periodEnd, pending, error, onCancel, onConfirm}: {
  confirm: Exclude<Confirm, {kind: "name"}>; current: PlanOption; label: (o: PlanOption) => string; periodEnd: string
  pending: boolean; error: unknown; onCancel: () => void; onConfirm: () => void
}) {
  const {t} = useTranslation();
  const common = {className: "mt-0", announce: true, pending, onCancel, onConfirm, cancelLabel: t("finance.plan.confirm.back"), error: error ? <FormErrorText error={error}/> : undefined};
  if (confirm.kind === "free") {
    return <InlineConfirm {...common} tone="danger" text={t("finance.plan.confirmFree.body", {date: periodEnd})} confirmLabel={t("finance.plan.confirmFree.submit")}/>;
  }
  const plan = label(confirm.option);
  if (confirm.kind === "schedule") {
    return <InlineConfirm {...common} tone="brand" text={t("finance.plan.confirmSchedule.body", {plan: label(current), date: periodEnd})} confirmLabel={t("finance.plan.confirmSchedule.submit")}/>;
  }
  const fixed = confirm.option.monthly !== null;
  return (
    <InlineConfirm {...common} tone="brand"
      text={fixed
        ? t("finance.plan.confirmChange.body", {plan, amount: money(confirm.option.monthly ?? 0)})
        : t("finance.plan.confirmChange.bodyMetered", {plan})}
      confirmLabel={fixed ? t("finance.plan.confirmChange.pay") : t("finance.plan.changeTo", {plan})}/>
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
        <p className="text-xl font-semibold">
          <Amount>{option.monthly === 0 ? t("finance.plan.free") : t("finance.plan.perMonth", {amount: money(option.monthly)})}</Amount>
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

function CardAction({option, isCurrent, isPendingTarget, pendingAt, state, current, label, primary, owing, busy, onAct, onKeep}: {
  option: PlanOption; isCurrent: boolean; isPendingTarget: boolean; pendingAt?: string; state: PlanState
  current: PlanOption; label: (o: PlanOption) => string; primary: boolean; owing: boolean; busy: boolean; onAct: () => void; onKeep: () => void
}) {
  const {t} = useTranslation();
  if (isCurrent) return null;
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
  // An unpaid invoice blocks any change (the server says so): the one action is paying it, above.
  if (owing) return <p className="text-sm text-muted-foreground">{t("finance.plan.owing.blocked")}</p>;
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
    <Section title={t("finance.plan.usage.title")} className="space-y-2">
      <p className="text-base font-medium tabular-nums">
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
    </Section>
  );
}

function PlanSkeleton() {
  const {t} = useTranslation();
  return (
    <div className="@container space-y-8" aria-busy role="status" aria-label={t("common.loading")}>
      <Skeleton className="h-7 w-32"/>
      <Skeleton className="h-28 w-full"/>
      <div className="grid gap-4 @lg:grid-cols-2 @5xl:grid-cols-4">
        {[0, 1, 2, 3].map(i => <Skeleton key={i} className="h-44 w-full"/>)}
      </div>
    </div>
  );
}
