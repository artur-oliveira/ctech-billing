"use client"

import {Button, EmptyState, Field, Input, Skeleton, Switch} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Repeat} from "lucide-react"
import {useEffect, useRef, useState} from "react"

import {ExceptionsFields, PatternFields} from "@/components/finance/ExpressionEditor"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Select} from "@/components/ui/Select"
import {messageFor} from "@/lib/api/client"
import {
  archiveRecurrence, createRecurrence, type FinanceCtx, financeKeys, listAccounts, listRecurrences,
  patchRecurrence, previewRecurrence,
} from "@/lib/api/finance"
import type {Account, Adjust, Direction, NewRecurrence, Occurrence, Recurrence, RecurrencePatch} from "@/lib/api/financeTypes"
import {defaultModel, describeModel, type EditorModel, fromExpression, toExpression, validate} from "@/lib/finance/expression"
import {todayIso} from "@/lib/finance/today"
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation"
import {useFinanceCtx, useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"
import {money, shortDate} from "@/lib/format"
import {formatMoneyInput, limitMoneyDecimals, parseMoney} from "@/lib/money"

const PREVIEW_COUNT = 6
const DEBOUNCE_MS = 300
const ADJUST_OPTIONS: {value: Adjust; label: string}[] = [
  {value: "roll_forward", label: "Passa para o próximo dia útil"},
  {value: "none", label: "Mantém a data"},
]
const touched = (c: FinanceCtx) => [financeKeys.recurrences(c.mode, c.space), [...financeKeys.all(c.mode, c.space), "projection"]]

function ruleOf(r: Recurrence): string {
  const model = fromExpression(r.expression)
  return model ? describeModel(model) : "Regra personalizada"
}

/**
 * F4 — recorrências: a list, and a side panel to create or edit one. The rule
 * editor previews the next occurrences while it is edited; the preview IS the
 * confirmation, before anything is saved.
 */
export function RecurrencesView() {
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const [panel, setPanel] = useState<{mode: "new"} | {mode: "edit"; rec: Recurrence} | null>(null)
  const recs = useQuery({queryKey: financeKeys.recurrences(ctx.mode, ctx.space), queryFn: () => listRecurrences(ctx)})
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)})
  const names = new Map((accounts.data?.data ?? []).map(a => [a.id, a.name]))
  const active = (recs.data?.data ?? []).filter(r => !r.archived)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Recorrências</h1>
        {can("finance.write") && !panel && (
          <Button variant="brand" size="sm" onClick={() => setPanel({mode: "new"})}>Nova recorrência</Button>
        )}
      </div>

      {panel && (
        <RecurrencePanel
          key={panel.mode === "edit" ? panel.rec.id : "new"}
          editing={panel.mode === "edit" ? panel.rec : undefined}
          accounts={accounts.data?.data ?? []}
          onDone={() => setPanel(null)}
        />
      )}

      {recs.isLoading ? (
        <div className="space-y-2" aria-busy><Skeleton className="h-4 w-full"/><Skeleton className="h-4 w-4/5"/></div>
      ) : recs.error ? (
        <ErrorBlock error={recs.error} onRetry={() => void recs.refetch()}/>
      ) : active.length === 0 ? (
        <EmptyState
          icon={<Repeat/>}
          title="Nenhuma recorrência"
          description="Cadastre o que se repete (aluguel, salário, assinaturas) e as contas de cada mês aparecem sozinhas em A pagar e a receber."
        />
      ) : (
        <ul className="divide-y divide-border border-y border-border">
          {active.map(r => (
            <RecurrenceRow key={r.id} rec={r} account={names.get(r.account_id)} onEdit={() => setPanel({mode: "edit", rec: r})}/>
          ))}
        </ul>
      )}
    </div>
  )
}

function RecurrenceRow({rec, account, onEdit}: {rec: Recurrence; account?: string; onEdit: () => void}) {
  const {can} = useFinanceSpaces()
  const [confirming, setConfirming] = useState(false)
  const archive = useFinanceMutation((c, _: void, key) => archiveRecurrence(c, rec.id, key), touched, () => setConfirming(false))
  return (
    <li className="py-2.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm text-foreground">{rec.description || "Sem descrição"}</p>
          <p className="text-xs text-muted-foreground">
            {ruleOf(rec)}{account ? ` • ${account}` : ""}{rec.auto_settle ? ` • ${rec.direction === "payable" ? "Pagamento" : "Recebimento"} automático` : ""}
          </p>
        </div>
        <span className="text-xs text-muted-foreground">{rec.direction === "payable" ? "A pagar" : "A receber"}</span>
        <span data-numeric className="w-28 text-right text-sm tabular-nums">{money(rec.amount)}</span>
        {can("finance.write") && (
          <div className="flex gap-1">
            <Button size="sm" variant="ghost" onClick={onEdit}>Editar</Button>
            <Button size="sm" variant="ghost" aria-expanded={confirming} onClick={() => setConfirming(v => !v)}>Encerrar</Button>
          </div>
        )}
      </div>
      {confirming && (
        <div className="mt-2 flex flex-wrap items-center gap-2 rounded-lg bg-surface p-3 text-sm motion-safe:animate-in motion-safe:fade-in">
          <p className="text-muted-foreground">Encerrar esta recorrência? Ela para de gerar contas; as contas já geradas continuam como estão.</p>
          <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>Manter</Button>
          <Button size="sm" variant="danger" disabled={archive.isPending} onClick={() => archive.mutate()}>Confirmar</Button>
          {archive.error && <p role="alert" className="w-full text-danger">{messageFor(archive.error)}</p>}
        </div>
      )}
    </li>
  )
}

/**
 * The next occurrences for a valid model, debounced and last-answer-wins: a
 * slow reply to an older edit never overwrites the preview of a newer one.
 */
function usePreview(ctx: FinanceCtx, model: EditorModel, start: string, end: string, adjust: Adjust, valid: boolean) {
  const [state, setState] = useState<{occ: Occurrence[]; loading: boolean; error: unknown}>({occ: [], loading: false, error: null})
  const seq = useRef(0)
  const body = JSON.stringify({expression: toExpression(model), start, end, adjust})
  useEffect(() => {
    if (!valid || !start) return
    const mine = ++seq.current
    const abort = new AbortController()
    const t = setTimeout(() => {
      setState(s => ({...s, loading: true}))
      const parsed = JSON.parse(body) as {expression: NewRecurrence["expression"]; start: string; end: string; adjust: Adjust}
      previewRecurrence(ctx, {
        expression: parsed.expression, start: parsed.start, end: parsed.end || undefined,
        business_day_adjust: parsed.adjust, from: todayIso() > parsed.start ? todayIso() : parsed.start, count: PREVIEW_COUNT,
      }, abort.signal)
        .then(r => { if (mine === seq.current) setState({occ: r.data, loading: false, error: null}) })
        .catch(e => { if (mine === seq.current && !abort.signal.aborted) setState({occ: [], loading: false, error: e}) })
    }, DEBOUNCE_MS)
    return () => {
      clearTimeout(t)
      abort.abort()
    }
  }, [ctx, body, valid, start])
  return state
}

function RecurrencePanel({editing, accounts, onDone}: {editing?: Recurrence; accounts: Account[]; onDone: () => void}) {
  const ctx = useFinanceCtx()
  const {can} = useFinanceSpaces()
  const [direction, setDirection] = useState<Direction>(editing?.direction ?? "payable")
  const [description, setDescription] = useState(editing?.description ?? "")
  const [amountText, setAmountText] = useState(editing ? formatMoneyInput(editing.amount) : "")
  const [category, setCategory] = useState(editing?.category_id ?? "")
  const [account, setAccount] = useState(editing?.account_id ?? "")
  const [start, setStart] = useState(editing?.start ?? todayIso())
  const [end, setEnd] = useState(editing?.end ?? "")
  const [adjust, setAdjust] = useState<Adjust>(editing?.business_day_adjust ?? "roll_forward")
  const [autoSettle, setAutoSettle] = useState(editing?.auto_settle ?? false)
  const [model, setModel] = useState<EditorModel>(() => (editing && fromExpression(editing.expression)) || defaultModel(todayIso()))

  const errors = editing ? [] : validate(model)
  const preview = usePreview(ctx, model, start, end, adjust, errors.length === 0)
  const amount = parseMoney(amountText)
  const cats = accounts.filter(a => a.class === (direction === "payable" ? "expense" : "income") && !a.archived && !a.system)
  const assets = accounts.filter(a => a.class === "asset" && !a.archived && !a.system)

  const create = useFinanceMutation((c, body: NewRecurrence, key) => createRecurrence(c, body, key), touched, onDone)
  const patch = useFinanceMutation((c, body: RecurrencePatch, key) => patchRecurrence(c, editing!.id, body, key), touched, onDone)
  const pending = create.isPending || patch.isPending
  const ready = amount !== null && category !== "" && account !== "" && errors.length === 0 && !pending
  const showAuto = can("finance.settle") || (editing?.auto_settle ?? false)

  function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!ready) return
    if (!editing) {
      create.mutate({
        direction, amount: amount!, category_id: category, account_id: account, description: description || undefined,
        expression: toExpression(model), start, end: end || undefined, business_day_adjust: adjust, auto_settle: autoSettle || undefined,
      })
      return
    }
    const body: RecurrencePatch = {}
    if (amount !== editing.amount) body.amount = amount!
    if (category !== editing.category_id) body.category_id = category
    if (account !== editing.account_id) body.account_id = account
    if (description !== (editing.description ?? "")) body.description = description
    if (autoSettle !== editing.auto_settle) body.auto_settle = autoSettle
    if (end && end !== (editing.end ?? "")) body.end = end
    if (Object.keys(body).length === 0) return onDone()
    patch.mutate(body)
  }

  const [more, setMore] = useState(false)
  const nextDates = preview.occ.slice(0, PREVIEW_COUNT)

  return (
    <aside aria-label={editing ? "Editar recorrência" : "Nova recorrência"} className="space-y-4 rounded-lg border border-border p-4 motion-safe:animate-in motion-safe:fade-in">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium text-foreground">{editing ? "Editar recorrência" : "Nova recorrência"}</h2>
        {!editing && (
          <div role="group" aria-label="Direção" className="flex items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5">
            {(["payable", "receivable"] as Direction[]).map(d => (
              <button key={d} type="button" aria-pressed={direction === d} onClick={() => { setDirection(d); setCategory("") }}
                className={`rounded-md px-3 py-1 text-sm ${direction === d ? "bg-background font-medium text-foreground shadow-sm" : "text-muted-foreground"}`}>
                {d === "payable" ? "A pagar" : "A receber"}
              </button>
            ))}
          </div>
        )}
      </div>
      <form className="space-y-4" onSubmit={submit}>
        <div className="grid items-start gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="Descrição" htmlFor="rc-desc" className="lg:col-span-2"><Input id="rc-desc" value={description} onChange={e => setDescription(e.target.value)}/></Field>
          <Field label="Valor" htmlFor="rc-amount" required><Input id="rc-amount" inputMode="decimal" placeholder="0,00" value={amountText} onChange={e => setAmountText(limitMoneyDecimals(e.target.value))}/></Field>
          <Field label="Categoria" htmlFor="rc-cat" required hint={cats.length === 0 ? `Nenhuma categoria de ${direction === "payable" ? "despesa" : "receita"}. Crie uma em Contas.` : undefined}>
            <Select id="rc-cat" value={category} onValueChange={setCategory} options={cats.map(a => ({value: a.id, label: a.name}))}/>
          </Field>
          <Field label={direction === "payable" ? "Pagar com" : "Receber em"} htmlFor="rc-acct" required hint={assets.length === 0 ? "Nenhuma conta. Crie uma em Contas." : undefined}>
            <Select id="rc-acct" value={account} onValueChange={setAccount} options={assets.map(a => ({value: a.id, label: a.name}))}/>
          </Field>
          {!editing && (
            <>
              <PatternFields model={model} start={start} errors={errors} onChange={setModel}/>
              <Field label="Começa em" htmlFor="rc-start"><Input id="rc-start" type="date" value={start} onChange={e => setStart(e.target.value)}/></Field>
            </>
          )}
          {editing && (
            <Field label="Termina em" htmlFor="rc-end-edit"><Input id="rc-end-edit" type="date" value={end} onChange={e => setEnd(e.target.value)}/></Field>
          )}
        </div>

        {editing ? (
          <p className="text-sm text-muted-foreground">
            <span className="text-foreground">{ruleOf(editing)}.</span> Para mudar a regra, encerre esta recorrência e crie outra; as contas já geradas continuam como estão.
          </p>
        ) : (
          <>
            <p className="text-sm" aria-live="polite">
              <span className="font-medium text-foreground">Próximas datas: </span>
              {errors.length > 0 ? (
                <span className="text-muted-foreground">corrija a regra para ver as datas.</span>
              ) : preview.error ? (
                <span role="alert" className="text-danger">{messageFor(preview.error)}</span>
              ) : nextDates.length === 0 ? (
                <span className="text-muted-foreground">calculando…</span>
              ) : (
                <span className="tabular-nums text-foreground">
                  {nextDates.map((o, i) => (
                    <span key={o.nominal}>
                      {i > 0 && <span className="text-muted-foreground"> • </span>}
                      {shortDate(o.nominal)}
                      {o.due !== o.nominal && <span className="text-muted-foreground">{` (paga em ${shortDate(o.due)})`}</span>}
                    </span>
                  ))}
                </span>
              )}
            </p>

            <div>
              <button type="button" aria-expanded={more} onClick={() => setMore(v => !v)}
                className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
                {more ? "Menos opções" : "Mais opções"}
              </button>
              {more && (
                <div className="mt-3 grid items-start gap-4 border-t border-border pt-4 lg:grid-cols-[1fr_1fr_2fr]">
                  <Field label="Termina em" htmlFor="rc-end" hint="Em branco, não termina."><Input id="rc-end" type="date" value={end} onChange={e => setEnd(e.target.value)}/></Field>
                  <Field label="Se cair em fim de semana ou feriado" htmlFor="rc-adjust">
                    <Select id="rc-adjust" value={adjust} onValueChange={v => setAdjust(v as Adjust)} options={ADJUST_OPTIONS}/>
                  </Field>
                  <ExceptionsFields model={model} errors={errors} onChange={setModel}/>
                </div>
              )}
            </div>
          </>
        )}

        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          {showAuto && (
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={autoSettle} onCheckedChange={setAutoSettle} disabled={!autoSettle && !can("finance.settle")}
                aria-label={direction === "payable" ? "Pagar automaticamente no vencimento" : "Receber automaticamente no vencimento"}/>
              {direction === "payable" ? "Pagar automaticamente no vencimento" : "Receber automaticamente no vencimento"}
            </label>
          )}
          <div className="flex gap-2">
            <Button type="submit" variant="brand" size="sm" disabled={!ready}>{editing ? "Salvar" : "Criar recorrência"}</Button>
            <Button type="button" variant="outline" size="sm" onClick={onDone}>Fechar</Button>
          </div>
        </div>
        {(create.error || patch.error) && <p role="alert" className="text-sm text-danger">{messageFor(create.error ?? patch.error)}</p>}
      </form>
    </aside>
  )
}
