"use client"

import {Button, Separator, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {ArrowLeft, Check} from "lucide-react"
import Link from "next/link"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {toast} from "sonner"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Money} from "@/components/portal/Money"
import {PixPanel} from "@/components/portal/PixPanel"
import {StatusBadge} from "@/components/portal/StatusBadge"
import {messageFor, statusOf} from "@/lib/api/client"
import {getInvoice, payInvoice, portalKeys} from "@/lib/api/portal"
import type {Invoice, PixPayment} from "@/lib/api/types"
import {longDate, money, period} from "@/lib/format"
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle"

/**
 * P3 — a fatura. What it is, how much, when, and the button to pay it.
 *
 * The id is a query parameter and not a path segment. This app builds with
 * `output: "export"` and ships as objects in S3, and a `[id]` segment cannot be
 * exported without `generateStaticParams` enumerating every invoice that will
 * ever exist. `/invoice?id=…` is one object that reads its subject at runtime.
 *
 * There is no timeline here, no payment attempts, no charge id and no
 * metadata. Not hidden — the portal payload does not carry them (ADR 0012).
 * The reader is not debugging a settlement; they are paying a bill.
 */
export default function InvoicePage() {
  // useSearchParams suspends during prerender, and a static export prerenders
  // everything. Without the boundary the build fails rather than the page.
  return (
    <Suspense fallback={<DetailSkeleton/>}>
      <Invoice/>
    </Suspense>
  )
}

function Invoice() {
  const id = useSearchParams().get("id") ?? ""
  const queryClient = useQueryClient()
  const [payment, setPayment] = useState<PixPayment | null>(null)

  const query = useQuery({
    queryKey: portalKeys.invoice(id),
    queryFn: () => getInvoice(id),
    enabled: id !== "",
  })

  const pay = useMutation({
    mutationFn: () => payInvoice(id),
    onSuccess: result => {
      // The pay response carries the invoice as well as the charge, so the
      // screen re-renders from it instead of showing a stale amount for one
      // round trip.
      queryClient.setQueryData(portalKeys.invoice(id), result.invoice)
      setPayment(result.payment)
    },
    onError: error => toast.error(messageFor(error)),
  })

  // Named by its number once there is one. Somebody paying two bills has two
  // tabs open, and "Fatura · CTech Billing" twice tells them nothing.
  useDocumentTitle(query.data?.number ? `Fatura nº ${query.data.number}` : null)

  // A bare /invoice with no id is the same dead end as an invoice that does not
  // exist, and saying so beats a query that never fires under a spinner.
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

  const invoice = query.data
  // The server's answer, not arithmetic on the amounts. `amount_due === 0 &&
  // amount_paid > 0` called a zero-total invoice unpaid — the Free plan, issued
  // and settled on the spot with nothing ever paid (ADR 0019) — and sent its
  // reader to the "not payable" block, which told them a bill marked *Paga* was
  // "ainda não aberta para pagamento".
  const settled = invoice.settled

  return (
    <div className="space-y-10">
      {/* The amount is the h1 and the invoice number is a caption above it.
          The reverse — "Fatura nº 1042" in heading weight over a smaller
          figure — puts the page's largest type on the one fact nobody came
          here to read. */}
      <header className="space-y-3">
        <BackLink/>
        <p className="text-sm text-muted-foreground">
          {invoice.number ? `Fatura nº ${invoice.number}` : "Fatura"}
        </p>
        <h1>
          <Money
            cents={settled ? invoice.total : invoice.amount_due}
            currency={invoice.currency}
            size="hero"
          />
        </h1>
        <StatusBadge state={invoice.state} tone={invoice.tone}/>
        {/* One date, the one that matters now: the due date while anything is
            owed, the payment date once nothing is. Everything else about the
            document — number, period, the other date — is in Detalhes at the
            foot, so this line stays a sentence rather than a record.

            An invoice settled before `paid_at` existed carries no date, and
            says the period instead of a wrong day. */}
        <p className="text-pretty text-sm text-muted-foreground">
          {settled
            ? invoice.paid_on
              ? `Pago em ${longDate(invoice.paid_on)}`
              : `Referente ao período de ${period(invoice.period.start, invoice.period.end)}`
            : `Vencimento em ${longDate(invoice.due_date)}`}
        </p>
      </header>

      {settled ? (
        <Receipt invoice={invoice}/>
      ) : payment ? (
        <PixPanel
          invoiceId={invoice.id}
          payment={payment}
          amount={invoice.amount_due}
          currency={invoice.currency}
          // The prefix, not this invoice's key: a settled charge changes the
          // home screen's pendência and the list's row too, and going back to
          // either one to find the bill still open reads as a lost payment.
          onPaid={() => queryClient.invalidateQueries({queryKey: portalKeys.invoices})}
          // Same mutation as the first attempt. The server shares one Collector
          // between the portal and the e-mailed link, so re-opening a charge is
          // its decision to make — not a second code this screen invents.
          onRegenerate={() => pay.mutate()}
          regenerating={pay.isPending}
        />
      ) : invoice.payable ? (
        <Button block onClick={() => pay.mutate()} disabled={pay.isPending}>
          {pay.isPending
            ? "Gerando o código…"
            : `Pagar ${money(invoice.amount_due, invoice.currency)} com PIX`}
        </Button>
      ) : (
        <NotPayable invoice={invoice}/>
      )}

      {/* Below the action, not above it. The breakdown is what somebody reads
          when the amount surprised them; the pay button is what everybody
          else came for, and it should not be under a table. */}
      <Lines invoice={invoice}/>

      <Facts invoice={invoice}/>
    </div>
  )
}

function NotFound() {
  return (
    <div className="space-y-6">
      <BackLink/>
      <div className="space-y-2">
        <h1 className="text-xl font-semibold tracking-[-0.01em] text-foreground">
          Fatura não encontrada
        </h1>
        <p className="text-sm text-muted-foreground">
          Ela pode ter sido cancelada, ou o endereço pode estar incompleto.
        </p>
      </div>
      <Button variant="outline" render={<Link href="/invoices"/>}>
        Ver todas as faturas
      </Button>
    </div>
  )
}

function BackLink() {
  return (
    <Link
      href="/invoices"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3.5"/>
      Faturas
    </Link>
  )
}

/**
 * The lines, with pro-rata named rather than merely present.
 *
 * An unexplained partial amount is the single most common "what is this
 * charge?" message a billing system receives, and the answer is always the
 * same sentence — so the screen says it instead of a person saying it later.
 */
function Lines({invoice}: { invoice: Invoice }) {
  if (!invoice.lines?.length) return null

  return (
    <section aria-labelledby="linhas" className="space-y-4">
      <h2 id="linhas" className="text-sm font-medium text-muted-foreground">
        O que está sendo cobrado
      </h2>
      <ul className="space-y-3">
        {invoice.lines.map((line, i) => (
          <li key={i} className="flex items-baseline justify-between gap-4">
            <div className="min-w-0">
              <p className="text-sm text-foreground">{line.description}</p>
              {line.proration && (
                <p className="mt-0.5 text-xs text-muted-foreground">
                  Proporcional aos dias usados neste período
                </p>
              )}
            </div>
            <Money cents={line.amount} currency={invoice.currency} className="text-sm"/>
          </li>
        ))}
      </ul>
      <Separator/>
      <div className="flex items-baseline justify-between gap-4">
        <span className="text-sm font-medium text-foreground">Total</span>
        <Money cents={invoice.total} currency={invoice.currency}/>
      </div>
      {(invoice.amount_paid ?? 0) > 0 && invoice.amount_due > 0 && (
        <div className="flex items-baseline justify-between gap-4">
          <span className="text-sm text-muted-foreground">Já pago</span>
          <span data-numeric className="text-sm text-muted-foreground">
            − {money(invoice.amount_paid!, invoice.currency)}
          </span>
        </div>
      )}
    </section>
  )
}

/**
 * The facts a person quotes when they ask about a charge: which document, for
 * what stretch of time, due when, paid when.
 *
 * A description list at the foot of the page rather than a panel at the top.
 * Nobody opens an invoice to read its number — but the person who has to
 * mention it to somebody else needs it on screen, and hunting for it is the
 * reason they write in instead.
 */
function Facts({invoice}: { invoice: Invoice }) {
  return (
    <section aria-labelledby="detalhes" className="space-y-4 border-t border-border pt-6">
      <h2 id="detalhes" className="text-sm font-medium text-muted-foreground">
        Detalhes
      </h2>
      <dl className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
        {invoice.number != null && <Fact label="Número" value={String(invoice.number)} numeric/>}
        <Fact label="Período" value={period(invoice.period.start, invoice.period.end)}/>
        {/* Whichever date the header did not use. The same date in both places
            reads as two facts, and the one this block owes the reader is the
            other one: what a paid invoice was due, or how an open one is paid.
            The method is named only while it is still an offer — the payload
            does not say how a settled invoice was paid, and a zero-total one
            was not paid at all. */}
        {invoice.settled ? (
          <Fact label="Vencimento" value={longDate(invoice.due_date)}/>
        ) : (
          invoice.payable && <Fact label="Forma de pagamento" value="PIX"/>
        )}
      </dl>
    </section>
  )
}

function Fact({label, value, numeric}: { label: string; value: string; numeric?: boolean }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd data-numeric={numeric ? "" : undefined} className="text-sm text-foreground">
        {value}
      </dd>
    </div>
  )
}

/**
 * The payoff. Paying a bill is the one moment on this portal that deserves to
 * be unmistakable, so the confirmation is a filled block and not the tinted
 * hairline note it started as — a person who has just transferred money should
 * not have to look twice to be sure it landed.
 */
function Receipt({invoice}: { invoice: Invoice }) {
  return (
    <div className="flex items-start gap-3 rounded-xl bg-success px-5 py-4 text-background">
      <Check aria-hidden className="mt-0.5 size-5 shrink-0"/>
      <div className="space-y-1">
        <p className="font-medium">Pagamento recebido</p>
        <p className="text-sm opacity-90">
          {/* A zero-total invoice is settled without a payment, so quoting an
              amount received would be inventing one. Both branches say the same
              thing — there is nothing left to do — in the words that are true. */}
          {invoice.total === 0 ? (
            "Nada a pagar nesta fatura."
          ) : (
            <>
              <span data-numeric>{money(invoice.amount_paid ?? invoice.total, invoice.currency)}</span>
              {" · esta fatura está quitada."}
            </>
          )}
        </p>
      </div>
    </div>
  )
}

/**
 * Not payable is several different situations, and they need different
 * sentences. "Pendente de acordo" in particular must never read as a dead end:
 * it is the one state whose resolution is a conversation with a person.
 *
 * A settled invoice never reaches here — it renders the receipt instead — which
 * is what the final branch depends on. It used to catch that case too, and told
 * somebody holding a paid Free-plan invoice that it was "ainda não aberta para
 * pagamento": the one reading that is both wrong and impossible to act on.
 */
function NotPayable({invoice}: { invoice: Invoice }) {
  const message = invoice.state.startsWith("Pendente")
    ? "Esta fatura está em negociação. Fale com a gente para combinar como quitar — não há nada a pagar por aqui enquanto isso."
    : invoice.state === "Cancelada"
      ? "Esta fatura foi cancelada. Não há nada a pagar, e nenhum valor foi cobrado."
      : "Esta fatura ainda está sendo preparada. Quando for emitida, o pagamento aparece aqui."

  return (
    <div className="rounded-xl border border-border bg-surface px-5 py-4">
      <p className="text-pretty text-sm text-foreground">{message}</p>
    </div>
  )
}

function DetailSkeleton() {
  return (
    <div className="space-y-10" aria-busy>
      <div className="space-y-3">
        <Skeleton className="h-4 w-24"/>
        <Skeleton className="h-4 w-28"/>
        <Skeleton className="h-9 w-52"/>
        <Skeleton className="h-5 w-32 rounded-full"/>
        <Skeleton className="h-4 w-72"/>
      </div>
      <Skeleton className="h-11 w-full rounded-lg"/>
      <div className="space-y-3">
        <Skeleton className="h-4 w-40"/>
        <Skeleton className="h-4 w-full"/>
        <Skeleton className="h-4 w-5/6"/>
      </div>
    </div>
  )
}
