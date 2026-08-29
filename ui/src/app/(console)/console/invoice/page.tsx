"use client"

import {Button, Modal, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ArrowLeft, Copy} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {toast} from "sonner"

import {CreditNoteDialog} from "@/components/console/CreditNoteDialog"
import {DownloadPDF} from "@/components/DownloadPDF"
import {InvoiceStatusBadge} from "@/components/console/InvoiceStatusBadge"
import {Timeline} from "@/components/console/Timeline"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor, statusOf} from "@/lib/api/client"
import {
  consoleKeys,
  finalizeInvoice,
  getConsoleInvoice,
  getConsoleInvoicePDF,
  voidInvoice,
} from "@/lib/api/console"
import type {ConsoleInvoiceDetail} from "@/lib/api/consoleTypes"
import {useMode} from "@/lib/console/useMode"
import {longDate, money, period, shortDate} from "@/lib/format"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/**
 * C3 — the invoice, and the most important screen in the product.
 *
 * Everything an operator is asked about a bill is answerable here without
 * leaving it: what it is, what it cost, what has been paid, what has been
 * credited, what happened to it and who did each of those things. The three
 * writes live at the top because a screen that answers "what happened" and
 * never "fix it" is a screen somebody reads and then opens a terminal.
 *
 * Which actions exist is decided by the status, not by disabling buttons: a
 * greyed-out "anular" on a paid invoice invites the question of what it would
 * have done, and the answer — nothing, ever — is better expressed by its
 * absence.
 */
export default function ConsoleInvoicePage() {
  return (
    <Suspense fallback={<DetailSkeleton/>}>
      <Detail/>
    </Suspense>
  )
}

function Detail() {
  const id = useSearchParams().get("id") ?? ""
  const mode = useMode()
  const queryClient = useQueryClient()
  const [voiding, setVoiding] = useState(false)
  const [crediting, setCrediting] = useState(false)

  const query = useQuery({
    queryKey: consoleKeys.invoice(mode, id),
    queryFn: () => getConsoleInvoice(id, mode),
    enabled: id !== "",
  })

  // Both writes answer with the whole detail, so the screen re-renders from the
  // response instead of refetching — and the list behind it is invalidated,
  // because the row there says the same thing.
  const write = (fn: (id: string) => Promise<ConsoleInvoiceDetail>, done: string) =>
    ({
      mutationFn: () => fn(id),
      onSuccess: (fresh: ConsoleInvoiceDetail) => {
        queryClient.setQueryData(consoleKeys.invoice(mode, id), fresh)
        void queryClient.invalidateQueries({queryKey: consoleKeys.invoices(mode)})
        toast.success(done)
      },
      onError: (error: unknown) => toast.error(messageFor(error)),
    })

  const finalize = useMutation(write(invoiceId => finalizeInvoice(invoiceId, mode), "Fatura emitida."))
  const cancel = useMutation({
    ...write(invoiceId => voidInvoice(invoiceId, mode), "Fatura anulada."),
    onSettled: () => setVoiding(false),
  })

  useDocumentTitle(query.data?.invoice.number ? `Fatura nº ${query.data.invoice.number}` : null)

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

  const {
    invoice,
    customer_name: customerName,
    credit_notes: notes = [],
    credited,
    fully_credited: fullyCredited,
  } = query.data
  const canFinalize = invoice.status === "DRAFT"
  const canVoid = invoice.status === "DRAFT" || invoice.status === "OPEN"
  const canCredit = ["OPEN", "PAID", "UNCOLLECTIBLE"].includes(invoice.status)

  return (
    <div className="space-y-8">
      <header className="space-y-3">
        <BackLink/>
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-2">
            <h1 data-numeric className="text-lg font-semibold tracking-[-0.01em] text-foreground">
              {invoice.number ? `Fatura nº ${invoice.number}` : "Fatura sem número"}
            </h1>
            <div className="flex flex-wrap items-center gap-2">
              <InvoiceStatusBadge invoice={invoice}/>
              {fullyCredited && (
                <span className="text-xs text-muted-foreground">Totalmente estornada</span>
              )}
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {canFinalize && (
              <Button size="sm" onClick={() => finalize.mutate()} disabled={finalize.isPending}>
                {finalize.isPending ? "Emitindo…" : "Emitir fatura"}
              </Button>
            )}
            {canCredit && (
              <Button variant="outline" size="sm" onClick={() => setCrediting(true)}>
                Emitir nota de crédito
              </Button>
            )}
            {invoice.checkout_url && <CopyLink url={invoice.checkout_url}/>}
            {/* Not on a draft: it has no number, so there is no document — and
                the server refuses rather than rendering something that looks
                official and refers to nothing. */}
            {invoice.status !== "DRAFT" && (
              <DownloadPDF fetchLink={() => getConsoleInvoicePDF(invoice.id, mode)}/>
            )}
            {canVoid && (
              <Button variant="outline" size="sm" onClick={() => setVoiding(true)}>
                Anular
              </Button>
            )}
          </div>
        </div>
      </header>

      {/* The money, opened into its parts. "Mostrar a derivação, não só o
          número" is the product's own rule, and this row is where it is paid
          for: total, paid, credited, and what is left. */}
      <section aria-labelledby="valores" className="space-y-3">
        <h2 id="valores" className="sr-only">Valores</h2>
        <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 sm:grid-cols-4">
          <Amount label="Total" cents={invoice.total} currency={invoice.currency} strong/>
          <Amount label="Pago" cents={invoice.amount_paid} currency={invoice.currency}/>
          <Amount label="Creditado" cents={credited} currency={invoice.currency}/>
          <Amount label="Em aberto" cents={invoice.amount_due} currency={invoice.currency} strong/>
        </dl>
        <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
          <Fact label="Período" value={period(invoice.period.start, invoice.period.end)}/>
          <Fact label="Vencimento" value={longDate(invoice.due_date)}/>
          <Fact
            label="Cliente"
            value={customerName || invoice.customer_id}
            href={`/console/customer?id=${invoice.customer_id}`}
          />
          {invoice.subscription_id && (
            <Fact
              label="Assinatura"
              value={invoice.subscription_id}
              href={`/console/subscription?id=${invoice.subscription_id}`}
            />
          )}
          <Fact label="Tentativas de cobrança" value={String(invoice.attempt_count)}/>
        </dl>
      </section>

      <section aria-labelledby="linhas" className="space-y-3">
        <h2 id="linhas" className="text-sm font-medium text-muted-foreground">Linhas</h2>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[36rem] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th scope="col" className="py-2 pr-4 font-medium">Descrição</th>
                <th scope="col" className="py-2 pr-4 font-medium">Período</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">Qtd.</th>
                <th scope="col" className="py-2 pr-4 text-right font-medium">Unitário</th>
                <th scope="col" className="py-2 text-right font-medium">Valor</th>
              </tr>
            </thead>
            <tbody>
              {(invoice.lines ?? []).map((line, i) => (
                <tr key={i} className="border-b border-border last:border-0">
                  <td className="py-2 pr-4 text-foreground">
                    {line.description}
                    {/* Pro-rata is named, not inferred from an odd amount. It is
                        the single most common "what is this charge?" question,
                        and the answer is always the same sentence. */}
                    {line.proration && (
                      <span className="ml-2 text-xs text-muted-foreground">proporcional</span>
                    )}
                  </td>
                  <td data-numeric className="py-2 pr-4 text-muted-foreground">
                    {line.period
                      ? `${shortDate(line.period.start)} – ${shortDate(line.period.end)}`
                      : "—"}
                  </td>
                  <td data-numeric className="py-2 pr-4 text-right text-muted-foreground">
                    {line.quantity}
                  </td>
                  <td data-numeric className="py-2 pr-4 text-right text-muted-foreground">
                    {money(line.unit_amount, invoice.currency)}
                  </td>
                  <td data-numeric className="py-2 text-right text-foreground">
                    {money(line.amount, invoice.currency)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {notes.length > 0 && (
        <section aria-labelledby="creditos" className="space-y-3">
          <h2 id="creditos" className="text-sm font-medium text-muted-foreground">
            Notas de crédito
          </h2>
          <ul className="divide-y divide-border border-y border-border">
            {notes.map(note => (
              <li key={note.id} className="flex flex-wrap items-baseline justify-between gap-3 py-2">
                <div className="min-w-0">
                  <p className="text-sm text-foreground">{note.reason}</p>
                  <p className="text-xs text-muted-foreground">
                    {note.created_by} · {shortDate(note.created_at.slice(0, 10))}
                    {note.refunded_externally && " · devolvido pelo wallet"}
                  </p>
                </div>
                <span data-numeric className="text-sm text-foreground">
                  {money(note.amount, invoice.currency)}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      <Timeline entries={query.data.timeline}/>

      <Modal
        open={voiding}
        onClose={() => setVoiding(false)}
        title="Anular esta fatura?"
        description={
          invoice.status === "OPEN"
            ? "A fatura deixa de ser cobrável e o cliente não pode mais pagá-la. Nada é apagado: ela continua visível, anulada, com todo o histórico."
            : "O rascunho é anulado e nunca chega a ser emitido."
        }
        cancelLabel="Manter"
        submitLabel="Anular fatura"
        danger
        loading={cancel.isPending}
        onSubmit={() => cancel.mutate()}
      />

      <CreditNoteDialog
        open={crediting}
        invoice={invoice}
        credited={credited}
        onClose={() => setCrediting(false)}
        onIssued={() => {
          void queryClient.invalidateQueries({queryKey: consoleKeys.invoice(mode, id)})
          setCrediting(false)
        }}
      />
    </div>
  )
}

function Amount({
  label,
  cents,
  currency,
  strong,
}: { label: string; cents: number; currency: string; strong?: boolean }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd
        data-numeric
        className={strong ? "text-base font-semibold text-foreground" : "text-base text-foreground"}
      >
        {money(cents, currency)}
      </dd>
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

/**
 * "Copiar link de pagamento", which is what an operator does when a customer
 * says they never received the bill.
 *
 * The URL comes from the server and is never built here: it is signed, and it
 * is absent unless the invoice is actually payable — so a console that
 * assembled it would be one invoice state away from sending somebody a 404.
 */
function CopyLink({url}: { url: string }) {
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(url)
          toast.success("Link de pagamento copiado.")
        } catch {
          // Clipboard access can be refused (an insecure origin, a permission
          // policy). Saying so beats a button that silently does nothing.
          toast.error("Não foi possível copiar. O link está no campo de endereço da fatura pública.")
        }
      }}
    >
      <Copy aria-hidden className="size-3.5"/>
      Copiar link
    </Button>
  )
}

function NotFound() {
  return (
    <div className="space-y-6">
      <BackLink/>
      <div className="space-y-2">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">
          Fatura não encontrada
        </h1>
        <p className="text-sm text-muted-foreground">
          Ela pode pertencer ao outro modo — confira se você está em Produção ou Teste.
        </p>
      </div>
    </div>
  )
}

function BackLink() {
  return (
    <Link
      href="/console/invoices"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      Faturas
    </Link>
  )
}

function DetailSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <div className="space-y-3">
        <Skeleton className="h-4 w-24"/>
        <Skeleton className="h-6 w-48"/>
        <Skeleton className="h-5 w-24 rounded-full"/>
      </div>
      <Skeleton className="h-20 w-full"/>
      <Skeleton className="h-32 w-full"/>
    </div>
  )
}
