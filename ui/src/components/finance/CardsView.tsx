"use client"

import {Badge, Button, Drawer, EmptyState, Field, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {AlertCircle, ChevronLeft, ChevronRight, CircleCheck, CircleDot, Clock, CreditCard, Lock} from "lucide-react"
import {useState} from "react"
import {useTranslation} from "react-i18next"

import {SettleForm} from "@/components/finance/BillsView"
import {CardBrandMark, maskedLast4} from "@/components/finance/CardBrandMark"
import {CardForm} from "@/components/finance/CardForm"
import {PurchasePanel} from "@/components/finance/PurchasePanel"
import {wholeSpace} from "@/components/finance/TransferPanel"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {
  advancePurchase, closeStatement, type FinanceCtx, financeKeys, getBill, getCardStatement, listAccounts, listCards, listPurchases,
  refundPurchase,
} from "@/lib/api/finance"
import type {Account, Card, CardStatement, CardStatementStatus, Purchase, StatementItem} from "@/lib/api/financeTypes"
import {currentLocale} from "@/lib/i18n"
import {monthShort, todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useCreateRequest} from "@/lib/finance/createRequest"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {shortDate, signedMoney} from "@/lib/format"
import {accountName} from "@/lib/finance/accountName"

type StatementState = CardStatementStatus | "overdue"

/**
 * A statement's badge. Open is the normal state of a card, so it is calm green
 * (positive), not a warning; closed and waiting for its due date, and paid, are
 * neutral; red (urgent) is kept for the one state that needs action: closed,
 * past its due date, unpaid. Each carries a glyph, never colour alone.
 */
const STATUS: Record<StatementState, {tone: "neutral" | "positive" | "urgent"; icon: typeof CircleDot}> = {
  open: {tone: "positive", icon: CircleDot},
  future: {tone: "neutral", icon: Clock},
  closed: {tone: "neutral", icon: Lock},
  paid: {tone: "neutral", icon: CircleCheck},
  overdue: {tone: "urgent", icon: AlertCircle},
}

/** "Vencida" is not a server status: a closed statement past its due date, unpaid. */
export function statementState(s: Pick<CardStatement, "status" | "due_date">, today: string): StatementState {
  return s.status === "closed" && s.due_date < today ? "overdue" : s.status
}

function shiftMonth(ym: string, n: number): string {
  const [y, m] = ym.split("-").map(Number)
  const i = y * 12 + (m - 1) + n
  return `${Math.floor(i / 12)}-${String((i % 12) + 1).padStart(2, "0")}`
}

type Panel = "new-card" | "edit-card" | "purchase" | null

/**
 * F5 — cartões. One card at a time, one statement at a time: the open one
 * first, then back to what was billed and forward to what will be. A purchase
 * is the whole expense on its date; the statements only show where each
 * installment is billed.
 */
export function CardsView({card: initial = ""}: {card?: string}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can, loading: verbsLoading} = useFinanceSpaces()
  const [picked, setPicked] = useState(initial)
  const [month, setMonth] = useState<string | null>(null)
  const [chosen, setChosen] = useState<Panel>(null)
  const [asked, setAsked] = useState(false)
  useCreateRequest("purchase", () => setAsked(true))

  const cards = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx)})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const active = (cards.data?.data ?? []).filter(c => !c.archived).sort((a, b) => a.name.localeCompare(b.name, currentLocale()))
  const card = active.find(c => c.id === picked) ?? active[0]
  const shown = month ?? card?.open_month ?? ""
  const all = accounts.data?.data ?? []
  // The central action on a phone: a purchase on the card on screen, or the
  // card itself when there is none to buy with yet.
  const loaded = !cards.isLoading && !accounts.isLoading && !verbsLoading
  const requested: Panel = asked && loaded
    ? (card ? (can("finance.write") ? "purchase" : null) : can("finance.configure") ? "new-card" : null)
    : null
  const panel = chosen ?? requested
  const setPanel = (p: Panel) => { setChosen(p); setAsked(false) }

  if (cards.isLoading || accounts.isLoading) {
    return <div className="space-y-2" aria-busy><Skeleton className="h-8 w-64"/><Skeleton className="h-4 w-full"/></div>
  }
  if (cards.error || accounts.error) {
    return <ErrorBlock error={cards.error ?? accounts.error} onRetry={() => { void cards.refetch(); void accounts.refetch() }}/>
  }
  if (!card) {
    return (
      <div className="space-y-4">
        <Drawer open={panel === "new-card"} onClose={() => setPanel(null)} title={t("finance.cards.new")}>
          <CardForm accounts={all} onDone={id => { setPanel(null); if (id) setPicked(id) }}/>
        </Drawer>
        {(
          <EmptyState
            icon={<CreditCard/>}
            title={t("finance.cards.empty")}
            description={can("finance.configure") ? undefined : t("finance.cards.emptyAsk")}
            action={can("finance.configure") ? <Button variant="brand" size="sm" onClick={() => setPanel("new-card")}>{t("finance.cards.new")}</Button> : undefined}
          />
        )}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <Field label={t("finance.cards.card")} htmlFor="cd-pick">
          <Select id="cd-pick" aria-label={t("finance.cards.card")} value={card.id} className="w-56"
            onValueChange={v => { setPicked(v); setMonth(null); setPanel(null) }}
            options={active.map(c => ({value: c.id, label: cardLabel(c), icon: <CardBrandMark brand={c.brand ?? "other"}/>}))}/>
        </Field>
        <div className="flex flex-wrap gap-2">
          {can("finance.write") && <Button variant="brand" size="sm" className="max-md:hidden" onClick={() => setPanel("purchase")}>{t("finance.cards.newPurchase")}</Button>}
          {can("finance.configure") && (
            <>
              <Button variant="ghost" size="sm" onClick={() => setPanel("edit-card")}>{t("finance.cards.edit")}</Button>
              <Button variant="ghost" size="sm" onClick={() => setPanel("new-card")}>{t("finance.cards.new")}</Button>
            </>
          )}
        </div>
      </div>

      <Drawer open={panel === "purchase"} onClose={() => setPanel(null)} title={t("finance.cards.newPurchase")} description={card.name}>
        <PurchasePanel cardId={card.id} accounts={all} onDone={() => setPanel(null)}/>
      </Drawer>
      <Drawer open={panel === "edit-card"} onClose={() => setPanel(null)} title={t("finance.cards.editNamed", {name: card.name})}>
        <CardForm key={card.id} card={card} accounts={all} onDone={() => setPanel(null)}/>
      </Drawer>
      <Drawer open={panel === "new-card"} onClose={() => setPanel(null)} title={t("finance.cards.new")}>
        <CardForm accounts={all} onDone={id => { setPanel(null); if (id) { setPicked(id); setMonth(null) } }}/>
      </Drawer>

      <CardHeader card={card}/>

      <StatementBlock key={`${card.id}-${shown}`} card={card} month={shown} accounts={all} onMonth={setMonth}/>
    </div>
  )
}

/** "Nubank •••• 4242" in a picker; the name alone when there are no digits. */
const cardLabel = (c: Card) => (c.last4 ? `${c.name} ${maskedLast4(c.last4)}` : c.name)

/** The card on screen: its mark, name and last digits, and when it closes and is due. */
function CardHeader({card}: {card: Card}) {
  const {t} = useTranslation()
  return (
    <div data-card-header className="flex items-center gap-3 border-b border-border pb-3">
      <CardBrandMark brand={card.brand ?? "other"} className="h-7 w-auto text-muted-foreground"/>
      <div className="min-w-0">
        <h1 className="flex flex-wrap items-baseline gap-x-2 text-lg font-semibold tracking-[-0.01em] text-foreground">
          <span className="min-w-0 [overflow-wrap:anywhere]">{card.name}</span>
          {card.last4 && <span className="text-sm font-normal tabular-nums text-muted-foreground">{maskedLast4(card.last4)}</span>}
        </h1>
        <p className="text-xs text-muted-foreground">{t("finance.cards.cycle", {closing: card.closing_day, due: card.due_day})}</p>
      </div>
    </div>
  )
}

function StatementBlock({card, month, accounts, onMonth}: {card: Card; month: string; accounts: Account[]; onMonth: (m: string) => void}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const q = useQuery({queryKey: financeKeys.cardStatement(ctx.mode, ctx.space, card.id, month), queryFn: () => getCardStatement(ctx, card.id, month)})
  const purchases = useQuery({queryKey: financeKeys.purchases(ctx.mode, ctx.space, card.id), queryFn: () => listPurchases(ctx, card.id)})
  const [action, setAction] = useState<"close" | "pay" | null>(null)
  const names = new Map(accounts.map(a => [a.id, accountName(a)]))
  const byId = new Map((purchases.data?.data ?? []).map(p => [p.id, p]))
  const s = q.data

  return (
    <section aria-label={t("finance.cards.statement")} className="space-y-3">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" aria-label={t("finance.cards.prev")} onClick={() => onMonth(shiftMonth(month, -1))}><ChevronLeft aria-hidden className="size-4"/></Button>
        <span className="min-w-16 text-center text-sm font-medium tabular-nums">{monthShort(month)}</span>
        <Button variant="ghost" size="sm" aria-label={t("finance.cards.next")} onClick={() => onMonth(shiftMonth(month, 1))}><ChevronRight aria-hidden className="size-4"/></Button>
      </div>
      {q.isLoading ? <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/></div> : q.error || !s ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : (
        <>
          <Summary statement={s}/>
          {s.status === "open" && can("finance.write") && action !== "close" && (
            <Button variant="outline" size="sm" onClick={() => setAction("close")}>{t("finance.cards.closeNow")}</Button>
          )}
          {s.status === "closed" && s.bill_id && can("finance.settle") && action !== "pay" && (
            <Button variant="brand" size="sm" onClick={() => setAction("pay")}>{t("finance.cards.pay")}</Button>
          )}
          {action === "close" && (
            <Confirm
              text={t("finance.cards.closeConfirm", {month: monthShort(month)})}
              run={(c, key) => closeStatement(c, card.id, month, key)}
              onDone={() => setAction(null)}
            />
          )}
          {action === "pay" && s.bill_id && <PayStatement billId={s.bill_id} accounts={accounts} onDone={() => setAction(null)}/>}
          {s.items.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">{t("finance.cards.noPurchases")}</p>
          ) : (
            <ul className="divide-y divide-border border-y border-border">
              {s.items.map(it => (
                <ItemRow key={`${it.purchase_id}-${it.kind}-${it.number ?? 0}`} item={it} card={card}
                  purchase={byId.get(it.purchase_id)} category={it.category_id ? names.get(it.category_id) : undefined}/>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  )
}

function Summary({statement: s}: {statement: CardStatement}) {
  const {t} = useTranslation()
  const closed = s.status === "closed" || s.status === "paid"
  const state = statementState(s, todayIso())
  const {tone, icon: Icon} = STATUS[state]
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-3 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <Badge tone={tone}><Icon aria-hidden/>{t(`finance.cards.status.${state}`)}</Badge>
        <span className="text-muted-foreground">{t(closed ? "finance.cards.closedOn" : "finance.cards.closesOn", {date: shortDate(s.closing_date)})}</span>
        <span className="text-muted-foreground">{t("finance.cards.dueOn", {date: shortDate(s.due_date)})}</span>
      </div>
      <span data-numeric className="text-base font-semibold tabular-nums">{signedMoney(s.total)}</span>
    </div>
  )
}

function PayStatement({billId, accounts, onDone}: {billId: string; accounts: Account[]; onDone: () => void}) {
  const ctx = useFinanceCtx()
  const bill = useQuery({queryKey: financeKeys.bill(ctx.mode, ctx.space, billId), queryFn: () => getBill(ctx, billId)})
  if (bill.isLoading) return <Skeleton className="h-16 w-full"/>
  if (bill.error || !bill.data) return <ErrorBlock error={bill.error} onRetry={() => void bill.refetch()}/>
  return <SettleForm bill={bill.data} accounts={accounts} onDone={onDone}/>
}

function ItemRow({item, card, purchase, category}: {item: StatementItem; card: Card; purchase?: Purchase; category?: string}) {
  const {t} = useTranslation()
  const {can} = useFinanceSpaces()
  const [action, setAction] = useState<"refund" | "advance" | null>(null)
  const actionable = item.kind === "installment" && purchase && !purchase.refunded && can("finance.write")
  const canAdvance = actionable && purchase.installments.some(i => i.month > card.open_month)
  return (
    <li className="py-2.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-foreground">{item.description}</p>
          <p className="text-xs text-muted-foreground">
            {shortDate(item.date)}{item.number && item.of ? ` · ${item.number}/${item.of}` : ""}{category ? ` · ${category}` : ""}
          </p>
        </div>
        <span data-numeric className="w-28 text-right text-sm tabular-nums">{signedMoney(item.amount)}</span>
        {actionable && (
          <div className="flex gap-1">
            {canAdvance && <Button size="sm" variant="ghost" onClick={() => setAction("advance")}>{t("finance.cards.advance")}</Button>}
            <Button size="sm" variant="ghost" onClick={() => setAction("refund")}>{t("finance.cards.refund")}</Button>
          </div>
        )}
      </div>
      {action === "refund" && purchase && (
        <Confirm
          text={t("finance.cards.refundConfirm")}
          run={(c, key) => refundPurchase(c, card.id, purchase.id, key)}
          onDone={() => setAction(null)}
        />
      )}
      {action === "advance" && purchase && (
        <Confirm
          text={t("finance.cards.advanceConfirm")}
          run={(c, key) => advancePurchase(c, card.id, purchase.id, key)}
          onDone={() => setAction(null)}
        />
      )}
    </li>
  )
}

function Confirm({text, run, onDone}: {text: string; run: (c: FinanceCtx, key: string) => Promise<unknown>; onDone: () => void}) {
  const {t} = useTranslation()
  const m = useFinanceMutation((c, _: void, key) => run(c, key), wholeSpace, onDone)
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
      <p className="text-muted-foreground">{text}</p>
      <Button size="sm" variant="outline" onClick={onDone}>{t("finance.cards.back")}</Button>
      <Button size="sm" variant="brand" disabled={m.isPending} onClick={() => m.mutate()}>{t("finance.cards.confirm")}</Button>
      {m.error ? <p role="alert" className="w-full text-sm text-danger">{messageFor(m.error)}</p> : null}
    </div>
  )
}
