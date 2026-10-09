"use client"

import {Badge, Button, EmptyState, Field, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {ChevronLeft, ChevronRight, CreditCard} from "lucide-react"
import {useState} from "react"

import {SettleForm} from "@/components/finance/BillsView"
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
import {monthShort} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {shortDate, signedMoney} from "@/lib/format"

const STATUS: Record<CardStatementStatus, {label: string; tone: "neutral" | "attention" | "urgent"}> = {
  open: {label: "Aberta", tone: "attention"},
  future: {label: "Futura", tone: "neutral"},
  closed: {label: "Fechada", tone: "urgent"},
  paid: {label: "Paga", tone: "neutral"},
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
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const [picked, setPicked] = useState(initial)
  const [month, setMonth] = useState<string | null>(null)
  const [panel, setPanel] = useState<Panel>(null)

  const cards = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx)})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const active = (cards.data?.data ?? []).filter(c => !c.archived).sort((a, b) => a.name.localeCompare(b.name, "pt-BR"))
  const card = active.find(c => c.id === picked) ?? active[0]
  const shown = month ?? card?.open_month ?? ""
  const all = accounts.data?.data ?? []

  if (cards.isLoading || accounts.isLoading) {
    return <div className="space-y-2" aria-busy><Skeleton className="h-8 w-64"/><Skeleton className="h-4 w-full"/></div>
  }
  if (cards.error || accounts.error) {
    return <ErrorBlock error={cards.error ?? accounts.error} onRetry={() => { void cards.refetch(); void accounts.refetch() }}/>
  }
  if (!card) {
    return (
      <div className="space-y-4">
        {panel === "new-card" ? <CardForm accounts={all} onDone={id => { setPanel(null); if (id) setPicked(id) }}/> : (
          <EmptyState
            icon={<CreditCard/>}
            title="Nenhum cartão ainda"
            description={can("finance.configure")
              ? "Cadastre um cartão com o dia em que a fatura fecha e o dia em que vence."
              : "Peça a quem administra o espaço para cadastrar um cartão."}
            action={can("finance.configure") ? <Button variant="brand" size="sm" onClick={() => setPanel("new-card")}>Novo cartão</Button> : undefined}
          />
        )}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <Field label="Cartão" htmlFor="cd-pick">
          <Select id="cd-pick" aria-label="Cartão" value={card.id} className="w-56"
            onValueChange={v => { setPicked(v); setMonth(null); setPanel(null) }}
            options={active.map(c => ({value: c.id, label: c.name}))}/>
        </Field>
        <div className="flex flex-wrap gap-2">
          {can("finance.write") && <Button variant="brand" size="sm" onClick={() => setPanel("purchase")}>Nova compra</Button>}
          {can("finance.configure") && (
            <>
              <Button variant="ghost" size="sm" onClick={() => setPanel("edit-card")}>Editar cartão</Button>
              <Button variant="ghost" size="sm" onClick={() => setPanel("new-card")}>Novo cartão</Button>
            </>
          )}
        </div>
      </div>

      {panel === "purchase" && <PurchasePanel cardId={card.id} accounts={all} onDone={() => setPanel(null)}/>}
      {panel === "edit-card" && <CardForm card={card} accounts={all} onDone={() => setPanel(null)}/>}
      {panel === "new-card" && <CardForm accounts={all} onDone={id => { setPanel(null); if (id) { setPicked(id); setMonth(null) } }}/>}

      <StatementBlock key={`${card.id}-${shown}`} card={card} month={shown} accounts={all} onMonth={setMonth}/>
    </div>
  )
}

function StatementBlock({card, month, accounts, onMonth}: {card: Card; month: string; accounts: Account[]; onMonth: (m: string) => void}) {
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const q = useQuery({queryKey: financeKeys.cardStatement(ctx.mode, ctx.space, card.id, month), queryFn: () => getCardStatement(ctx, card.id, month)})
  const purchases = useQuery({queryKey: financeKeys.purchases(ctx.mode, ctx.space, card.id), queryFn: () => listPurchases(ctx, card.id)})
  const [action, setAction] = useState<"close" | "pay" | null>(null)
  const names = new Map(accounts.map(a => [a.id, a.name]))
  const byId = new Map((purchases.data?.data ?? []).map(p => [p.id, p]))
  const s = q.data

  return (
    <section aria-label="Fatura" className="space-y-3">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" aria-label="Fatura anterior" onClick={() => onMonth(shiftMonth(month, -1))}><ChevronLeft aria-hidden className="size-4"/></Button>
        <span className="min-w-16 text-center text-sm font-medium tabular-nums">{monthShort(month)}</span>
        <Button variant="ghost" size="sm" aria-label="Próxima fatura" onClick={() => onMonth(shiftMonth(month, 1))}><ChevronRight aria-hidden className="size-4"/></Button>
      </div>
      {q.isLoading ? <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/></div> : q.error || !s ? (
        <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>
      ) : (
        <>
          <Summary statement={s}/>
          {s.status === "open" && can("finance.write") && action !== "close" && (
            <Button variant="outline" size="sm" onClick={() => setAction("close")}>Fechar fatura agora</Button>
          )}
          {s.status === "closed" && s.bill_id && can("finance.settle") && action !== "pay" && (
            <Button variant="brand" size="sm" onClick={() => setAction("pay")}>Pagar fatura</Button>
          )}
          {action === "close" && (
            <Confirm
              text={`Fechar a fatura de ${monthShort(month)} agora? Compras novas entram na próxima.`}
              run={(c, key) => closeStatement(c, card.id, month, key)}
              onDone={() => setAction(null)}
            />
          )}
          {action === "pay" && s.bill_id && <PayStatement billId={s.bill_id} accounts={accounts} onDone={() => setAction(null)}/>}
          {s.items.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">Nenhuma compra nesta fatura.</p>
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
  const st = STATUS[s.status]
  const closed = s.status === "closed" || s.status === "paid"
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-3 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <Badge tone={st.tone}>{st.label}</Badge>
        <span className="text-muted-foreground">{closed ? "Fechou em" : "Fecha em"} {shortDate(s.closing_date)}</span>
        <span className="text-muted-foreground">Vence em {shortDate(s.due_date)}</span>
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
            {canAdvance && <Button size="sm" variant="ghost" onClick={() => setAction("advance")}>Antecipar parcelas</Button>}
            <Button size="sm" variant="ghost" onClick={() => setAction("refund")}>Estornar compra</Button>
          </div>
        )}
      </div>
      {action === "refund" && purchase && (
        <Confirm
          text="Estornar esta compra? As parcelas já cobradas voltam como crédito na fatura aberta; as futuras deixam de existir."
          run={(c, key) => refundPurchase(c, card.id, purchase.id, key)}
          onDone={() => setAction(null)}
        />
      )}
      {action === "advance" && purchase && (
        <Confirm
          text="Trazer as parcelas restantes para a fatura aberta?"
          run={(c, key) => advancePurchase(c, card.id, purchase.id, key)}
          onDone={() => setAction(null)}
        />
      )}
    </li>
  )
}

function Confirm({text, run, onDone}: {text: string; run: (c: FinanceCtx, key: string) => Promise<unknown>; onDone: () => void}) {
  const m = useFinanceMutation((c, _: void, key) => run(c, key), wholeSpace, onDone)
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
      <p className="text-muted-foreground">{text}</p>
      <Button size="sm" variant="outline" onClick={onDone}>Voltar</Button>
      <Button size="sm" variant="brand" disabled={m.isPending} onClick={() => m.mutate()}>Confirmar</Button>
      {m.error ? <p role="alert" className="w-full text-sm text-danger">{messageFor(m.error)}</p> : null}
    </div>
  )
}
