"use client"

import {Badge, Button, EmptyState, Field, Input, Skeleton, Switch} from "@aoctech/ui"
import {useQuery, useQueryClient} from "@tanstack/react-query"
import {AlertCircle, CalendarClock, Clock, Receipt} from "lucide-react"
import {useEffect, useState} from "react"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {DateField} from "@/components/ui/DateField"
import {Select} from "@/components/ui/Select"
import {messageFor, statusOf} from "@/lib/api/client"
import {cancelBill, createBill, financeKeys, listAccounts, listBills, patchBill, settleBill} from "@/lib/api/finance"
import type {Account, Bill, BillPatch, Bucket, Direction, NewBill, Settlement} from "@/lib/api/financeTypes"
import {BUCKET_LABEL} from "@/lib/finance/labels"
import {monthLabel, todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {type FinanceCtx} from "@/lib/api/finance"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate} from "@/lib/format"
import {formatMoneyInput, limitMoneyDecimals, parseMoney} from "@/lib/money"

const GROUPS: {bucket: Bucket; title: string}[] = [
  {bucket: "overdue", title: "Vencidas"},
  {bucket: "today", title: "Vencem hoje"},
  {bucket: "upcoming", title: "A vencer"},
]
/**
 * Settling, said per direction and in plain words. Not "dar baixa" (ERP jargon
 * a person managing their own money does not use) and not "lançar" (which is
 * recording an entry — creating the bill — not paying it).
 */
const SETTLE_LABEL: Record<Direction, {action: string; confirm: string; auto: string; autoNote: string}> = {
  payable: {action: "Pagar", confirm: "Confirmar pagamento", auto: "Pagar automaticamente no vencimento", autoNote: "Pagamento automático"},
  receivable: {action: "Receber", confirm: "Confirmar recebimento", auto: "Receber automaticamente no vencimento", autoNote: "Recebimento automático"},
}

const BADGE: Record<Bucket, {tone: "urgent" | "attention" | "neutral"; icon: typeof Clock}> = {
  overdue: {tone: "urgent", icon: AlertCircle},
  today: {tone: "attention", icon: Clock},
  upcoming: {tone: "neutral", icon: CalendarClock},
}

/** Everything a write here changes: the list, the balances it moves, the projection. */
const touched = (c: FinanceCtx) => [
  financeKeys.bills(c.mode, c.space, "payable"),
  financeKeys.bills(c.mode, c.space, "receivable"),
  financeKeys.accounts(c.mode, c.space),
  ["finance", c.mode, ...financeKeys.all(c.mode, c.space).slice(2), "projection"],
]

/**
 * F2 — a pagar e a receber.
 *
 * One table per direction, grouped overdue / today / upcoming with a subtotal,
 * in the order the API returns (earliest due first, so overdue leads). A row's
 * actions open in place; the list never disappears behind a modal.
 */
export function BillsView() {
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const [direction, setDirection] = useState<Direction>("payable")
  const [creating, setCreating] = useState(false)

  const bills = useQuery({queryKey: financeKeys.bills(ctx.mode, ctx.space, direction), queryFn: () => listBills(ctx, direction)})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const rows = bills.data?.data ?? []
  const names = new Map((accounts.data?.data ?? []).map(a => [a.id, a.name]))

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div role="group" aria-label="Direção" className="flex items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5">
          {(["payable", "receivable"] as Direction[]).map(d => (
            <button
              key={d}
              type="button"
              aria-pressed={direction === d}
              onClick={() => setDirection(d)}
              className={`rounded-md px-3 py-1 text-sm transition-colors ${direction === d ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
            >
              {d === "payable" ? "A pagar" : "A receber"}
            </button>
          ))}
        </div>
        {can("finance.write") && !creating && (
          <Button variant="brand" size="sm" onClick={() => setCreating(true)}>Nova conta</Button>
        )}
      </div>

      <div className={creating ? "grid gap-6 lg:grid-cols-[1fr_22rem]" : ""}>
        <div className="min-w-0 space-y-6">
          {bills.isLoading ? (
            <div className="space-y-2" aria-busy><Skeleton className="h-5 w-32"/><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
          ) : bills.error ? (
            <ErrorBlock error={bills.error} onRetry={() => void bills.refetch()}/>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={<Receipt/>}
              title={direction === "payable" ? "Nenhuma conta a pagar em aberto" : "Nenhuma conta a receber em aberto"}
              description="Registre uma conta para acompanhar o vencimento e marcar quando pagar ou receber."
            />
          ) : (
            GROUPS.map(g => {
              const group = rows.filter(b => (b.bucket ?? "upcoming") === g.bucket)
              if (group.length === 0) return null
              const total = group.reduce((sum, b) => sum + b.amount, 0)
              return (
                <section key={g.bucket} aria-labelledby={`grp-${g.bucket}`} className="space-y-2">
                  <div className="flex items-baseline justify-between">
                    <h2 id={`grp-${g.bucket}`} className="text-sm font-medium text-foreground">{g.title}</h2>
                    <span data-numeric className="text-sm tabular-nums text-muted-foreground">{money(total)}</span>
                  </div>
                  <ul className="divide-y divide-border border-y border-border">
                    {group.map(b => (
                      <BillRow key={b.id} bill={b} accountName={names.get(b.account_id)} accounts={accounts.data?.data ?? []}/>
                    ))}
                  </ul>
                </section>
              )
            })
          )}
        </div>
        {creating && <NewBillPanel direction={direction} accounts={accounts.data?.data ?? []} onDone={() => setCreating(false)}/>}
      </div>
    </div>
  )
}

type Panel = "settle" | "edit" | "cancel" | null

function BillRow({bill, accountName, accounts}: {bill: Bill; accountName?: string; accounts: Account[]}) {
  const {can} = useFinanceSpaces()
  const [panel, setPanel] = useState<Panel>(null)
  const bucket = bill.bucket ?? "upcoming"
  const Icon = BADGE[bucket].icon
  return (
    <li className="py-2.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-foreground">{bill.description || "Sem descrição"}</p>
          <p className="text-xs text-muted-foreground">
            Vence {shortDate(bill.due_date)}{accountName ? ` · ${accountName}` : ""}{bill.auto_settle ? ` · ${SETTLE_LABEL[bill.direction].autoNote}` : ""}
          </p>
        </div>
        <Badge tone={BADGE[bucket].tone}><Icon aria-hidden className="size-3"/>{BUCKET_LABEL[bucket]}</Badge>
        <span data-numeric className="w-28 text-right text-sm tabular-nums text-foreground">{money(bill.amount)}</span>
        <div className="flex gap-1">
          {can("finance.settle") && (
            <Button size="sm" variant={panel === "settle" ? "outline" : "ghost"} aria-expanded={panel === "settle"} onClick={() => setPanel(panel === "settle" ? null : "settle")}>
              {SETTLE_LABEL[bill.direction].action}
            </Button>
          )}
          {can("finance.write") && (
            <>
              <Button size="sm" variant="ghost" aria-expanded={panel === "edit"} onClick={() => setPanel(panel === "edit" ? null : "edit")}>Editar</Button>
              <Button size="sm" variant="ghost" aria-expanded={panel === "cancel"} onClick={() => setPanel(panel === "cancel" ? null : "cancel")}>Cancelar conta</Button>
            </>
          )}
        </div>
      </div>
      {panel === "settle" && <SettleForm bill={bill} accounts={accounts} onDone={() => setPanel(null)}/>}
      {panel === "edit" && <EditForm bill={bill} accounts={accounts} onDone={() => setPanel(null)}/>}
      {panel === "cancel" && <CancelConfirm bill={bill} onDone={() => setPanel(null)}/>}
    </li>
  )
}

function useConflictReload(error: unknown) {
  const client = useQueryClient()
  const ctx = useFinanceCtx()
  const conflict = statusOf(error) === 409
  useEffect(() => {
    if (conflict) for (const key of touched(ctx)) void client.invalidateQueries({queryKey: key})
  }, [conflict, client, ctx])
  return conflict
}

function FormError({error}: {error: unknown}) {
  const conflict = useConflictReload(error)
  if (!error) return null
  return (
    <p role="alert" className="w-full text-sm text-danger">
      {conflict ? "Esta conta mudou enquanto você a via; recarregamos a lista. Confira e tente de novo." : messageFor(error)}
    </p>
  )
}

// items-start: each Field is itself a grid; stretched to the row's height, the
// spare space goes to its label row and pushes the input down out of line with
// the field beside it (the one with a hint is taller).
const inPanel = "mt-3 grid items-start gap-3 rounded-lg bg-surface p-3 sm:grid-cols-2 motion-safe:animate-in motion-safe:fade-in"

function SettleForm({bill, accounts, onDone}: {bill: Bill; accounts: Account[]; onDone: () => void}) {
  const [amountText, setAmountText] = useState(formatMoneyInput(bill.amount))
  const [date, setDate] = useState(todayIso())
  const [category, setCategory] = useState("")
  const settle = useFinanceMutation((c, body: Settlement, key) => settleBill(c, bill.id, body, key), touched, onDone)

  // The amount is always visible and starts at the bill's own: paying exactly
  // what was owed is the common case, and a different amount is just an edit
  // of the same field — no extra toggle to find.
  const paid = parseMoney(amountText)
  const gap = paid === null ? 0 : paid - bill.amount
  const categories = accounts.filter(a => (a.class === "income" || a.class === "expense") && !a.system && !a.archived)
  const catName = categories.find(a => a.id === category)?.name
  const ready = paid !== null && (gap === 0 || category !== "") && !settle.isPending

  return (
    <form
      className={inPanel}
      onSubmit={e => {
        e.preventDefault()
        if (!ready) return
        const body: Settlement = {paid_date: date}
        if (gap !== 0) {
          body.paid_amount = paid!
          body.difference_category_id = category
        }
        settle.mutate(body)
      }}
    >
      <Field label={bill.direction === "payable" ? "Data do pagamento" : "Data do recebimento"} htmlFor={`d-${bill.id}`}>
        <DateField id={`d-${bill.id}`} value={date} onValueChange={setDate}/>
      </Field>
      <Field label={bill.direction === "payable" ? "Valor pago" : "Valor recebido"} htmlFor={`v-${bill.id}`}>
        <Input id={`v-${bill.id}`} inputMode="decimal" value={amountText} onChange={e => setAmountText(limitMoneyDecimals(e.target.value))} aria-invalid={paid === null}/>
      </Field>
      {gap !== 0 && (
        <>
          <Field label="Categoria da diferença" htmlFor={`c-${bill.id}`}>
            <Select id={`c-${bill.id}`} value={category} onValueChange={setCategory} options={categories.map(a => ({value: a.id, label: a.name}))}/>
          </Field>
          <p className="text-sm text-muted-foreground sm:pt-7">
            {money(Math.abs(gap))} {gap > 0 ? "a mais" : "a menos"} que a conta ({money(bill.amount)})
            {catName ? `; registrado em ${catName}` : "; escolha onde registrar a diferença"}
          </p>
        </>
      )}
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={!ready}>{SETTLE_LABEL[bill.direction].confirm}</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>Fechar</Button>
        <FormError error={settle.error}/>
      </div>
    </form>
  )
}

function EditForm({bill, accounts, onDone}: {bill: Bill; accounts: Account[]; onDone: () => void}) {
  const {can} = useFinanceSpaces()
  const [description, setDescription] = useState(bill.description ?? "")
  const [amountText, setAmountText] = useState(formatMoneyInput(bill.amount))
  const [due, setDue] = useState(bill.due_date)
  const [category, setCategory] = useState(bill.category_id)
  const [account, setAccount] = useState(bill.account_id)
  const [autoSettle, setAutoSettle] = useState(bill.auto_settle)
  const edit = useFinanceMutation((c, body: BillPatch, key) => patchBill(c, bill.id, body, key), touched, onDone)
  const amount = parseMoney(amountText)
  const cats = accounts.filter(a => a.class === (bill.direction === "payable" ? "expense" : "income") && !a.archived && !a.system)
  const assets = accounts.filter(a => a.class === "asset" && !a.archived && !a.system)
  // Turning auto-settle ON is settling; turning it off only removes power.
  const showAuto = can("finance.settle") || bill.auto_settle

  return (
    <form
      className={inPanel}
      onSubmit={e => {
        e.preventDefault()
        if (amount === null) return
        const body: BillPatch = {}
        if (description !== (bill.description ?? "")) body.description = description
        if (amount !== bill.amount) body.amount = amount
        if (due !== bill.due_date) body.due_date = due
        if (category !== bill.category_id) body.category_id = category
        if (account !== bill.account_id) body.account_id = account
        if (autoSettle !== bill.auto_settle) body.auto_settle = autoSettle
        if (Object.keys(body).length === 0) return onDone()
        edit.mutate(body)
      }}
    >
      <Field label="Descrição" htmlFor={`ed-${bill.id}`}><Input id={`ed-${bill.id}`} value={description} onChange={e => setDescription(e.target.value)}/></Field>
      <Field label="Valor" htmlFor={`ev-${bill.id}`}><Input id={`ev-${bill.id}`} inputMode="decimal" value={amountText} onChange={e => setAmountText(limitMoneyDecimals(e.target.value))} aria-invalid={amount === null}/></Field>
      <Field label="Vencimento" htmlFor={`eu-${bill.id}`}><DateField id={`eu-${bill.id}`} value={due} onValueChange={setDue}/></Field>
      <Field label="Categoria" htmlFor={`ec-${bill.id}`}>
        <Select id={`ec-${bill.id}`} value={category} onValueChange={setCategory} options={cats.map(a => ({value: a.id, label: a.name}))}/>
      </Field>
      <Field label={bill.direction === "payable" ? "Pagar com" : "Receber em"} htmlFor={`ea-${bill.id}`}>
        <Select id={`ea-${bill.id}`} value={account} onValueChange={setAccount} options={assets.map(a => ({value: a.id, label: a.name}))}/>
      </Field>
      {showAuto && (
        <label className="flex items-center gap-2 self-end text-sm">
          <Switch checked={autoSettle} onCheckedChange={setAutoSettle} disabled={!autoSettle && !can("finance.settle")} aria-label={SETTLE_LABEL[bill.direction].auto}/>
          {SETTLE_LABEL[bill.direction].auto}
        </label>
      )}
      <div className="flex flex-wrap items-center gap-2 sm:col-span-2">
        <Button type="submit" variant="brand" size="sm" disabled={amount === null || edit.isPending}>Salvar</Button>
        <Button type="button" variant="outline" size="sm" onClick={onDone}>Fechar</Button>
        <FormError error={edit.error}/>
      </div>
    </form>
  )
}

function CancelConfirm({bill, onDone}: {bill: Bill; onDone: () => void}) {
  const cancel = useFinanceMutation((c, _: void, key) => cancelBill(c, bill.id, key), touched, onDone)
  return (
    <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
      <p className="text-muted-foreground">
        Cancelar “{bill.description || "esta conta"}”? Ela sai da lista e do resultado de {monthLabel(bill.competence_date)}.
      </p>
      <Button size="sm" variant="outline" onClick={onDone}>Manter</Button>
      <Button size="sm" variant="danger" disabled={cancel.isPending} onClick={() => cancel.mutate()}>Cancelar conta</Button>
      <FormError error={cancel.error}/>
    </div>
  )
}

function NewBillPanel({direction, accounts, onDone}: {direction: Direction; accounts: Account[]; onDone: () => void}) {
  const {can} = useFinanceSpaces()
  const [description, setDescription] = useState("")
  const [amountText, setAmountText] = useState("")
  const [due, setDue] = useState(todayIso())
  const [competence, setCompetence] = useState("")
  const cats = accounts.filter(a => a.class === (direction === "payable" ? "expense" : "income") && !a.archived && !a.system)
  const assets = accounts.filter(a => a.class === "asset" && !a.archived && !a.system)
  const [category, setCategory] = useState("")
  const [account, setAccount] = useState("")
  const [autoSettle, setAutoSettle] = useState(false)
  const create = useFinanceMutation((c, body: NewBill, key) => createBill(c, body, key), touched, onDone)
  const amount = parseMoney(amountText)
  const ready = amount !== null && category !== "" && account !== "" && due !== "" && !create.isPending

  return (
    <aside aria-label={direction === "payable" ? "Nova conta a pagar" : "Nova conta a receber"} className="h-fit space-y-3 rounded-lg border border-border p-4 motion-safe:animate-in motion-safe:fade-in">
      <h2 className="text-sm font-medium text-foreground">{direction === "payable" ? "Nova conta a pagar" : "Nova conta a receber"}</h2>
      <form
        className="space-y-3"
        onSubmit={e => {
          e.preventDefault()
          if (!ready) return
          create.mutate({
            direction, amount: amount!, category_id: category, account_id: account, description: description || undefined,
            due_date: due, competence_date: competence || undefined, auto_settle: autoSettle || undefined,
          })
        }}
      >
        <Field label="Descrição" htmlFor="nb-desc"><Input id="nb-desc" value={description} onChange={e => setDescription(e.target.value)} autoFocus/></Field>
        <Field label="Valor" htmlFor="nb-amount" required><Input id="nb-amount" inputMode="decimal" placeholder="0,00" value={amountText} onChange={e => setAmountText(limitMoneyDecimals(e.target.value))}/></Field>
        <Field label="Vencimento" htmlFor="nb-due" required><DateField id="nb-due" value={due} onValueChange={setDue}/></Field>
        <Field label="Competência (DRE)" htmlFor="nb-comp" hint="Em branco, vale o vencimento.">
          <DateField id="nb-comp" value={competence} onValueChange={setCompetence} placeholder="Igual ao vencimento"/>
        </Field>
        <Field label="Categoria" htmlFor="nb-cat" required hint={cats.length === 0 ? `Nenhuma categoria de ${direction === "payable" ? "despesa" : "receita"}. Crie uma em Contas.` : undefined}>
          <Select id="nb-cat" value={category} onValueChange={setCategory} options={cats.map(a => ({value: a.id, label: a.name}))}/>
        </Field>
        <Field label={direction === "payable" ? "Pagar com" : "Receber em"} htmlFor="nb-acct" required hint={assets.length === 0 ? "Nenhuma conta. Crie uma em Contas." : undefined}>
          <Select id="nb-acct" value={account} onValueChange={setAccount} options={assets.map(a => ({value: a.id, label: a.name}))}/>
        </Field>
        {can("finance.settle") && (
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={autoSettle} onCheckedChange={setAutoSettle} aria-label={SETTLE_LABEL[direction].auto}/>
            {SETTLE_LABEL[direction].auto}
          </label>
        )}
        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="brand" size="sm" disabled={!ready}>Registrar</Button>
          <Button type="button" variant="outline" size="sm" onClick={onDone}>Fechar</Button>
        </div>
        <FormError error={create.error}/>
      </form>
    </aside>
  )
}
