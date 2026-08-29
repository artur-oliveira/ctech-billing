"use client"

import {Button, EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {ChevronLeft, ChevronRight, FileText} from "lucide-react"
import Link from "next/link"
import {useState} from "react"

import {InvoiceStatusBadge} from "@/components/console/InvoiceStatusBadge"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {consoleKeys, listConsoleInvoices} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"
import {money, shortDate} from "@/lib/format"

/**
 * C2 — the invoice list, and the screen an operator lives in.
 *
 * It is a real table, not the portal's stack of rows: the whole job here is
 * comparing a column down the page — who owes what, due when — and a card per
 * invoice makes that impossible by construction.
 *
 * Paged by **month** rather than by an open-ended cursor, because that is the
 * partition the API indexes (`ListByMonth`) and, more to the point, because
 * "faturas de agosto" is how an operator asks. A cursor inside the month is
 * still there for the tenant that outgrows one page.
 */
export default function ConsoleInvoicesPage() {
  const mode = useMode()
  const today = new Date()
  const [year, setYear] = useState(today.getFullYear())
  const [month, setMonth] = useState(today.getMonth() + 1)

  const query = useQuery({
    queryKey: consoleKeys.invoiceMonth(mode, year, month),
    queryFn: () => listConsoleInvoices(year, month, undefined, mode),
  })

  const invoices = query.data?.data ?? []
  const label = new Intl.DateTimeFormat("pt-BR", {month: "long", year: "numeric"})
    .format(new Date(year, month - 1, 1))

  function shift(by: number) {
    const d = new Date(year, month - 1 + by, 1)
    setYear(d.getFullYear())
    setMonth(d.getMonth() + 1)
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Faturas</h1>

        {/* The period control is the filter, so it sits with the title rather
            than above the table: it decides what the table *is*, not how it is
            sorted. */}
        <div className="flex items-center gap-1">
          <Button variant="outline" size="sm" aria-label="Mês anterior" onClick={() => shift(-1)}>
            <ChevronLeft aria-hidden className="size-4"/>
          </Button>
          <span className="min-w-40 text-center text-sm text-foreground first-letter:uppercase">
            {label}
          </span>
          <Button variant="outline" size="sm" aria-label="Próximo mês" onClick={() => shift(1)}>
            <ChevronRight aria-hidden className="size-4"/>
          </Button>
        </div>
      </div>

      {query.isPending && <TableSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && invoices.length === 0 && (
        <EmptyState
          icon={<FileText/>}
          title="Nenhuma fatura neste mês"
          description="As faturas aparecem aqui no dia em que são emitidas. Use as setas para olhar outro mês."
        />
      )}

      {invoices.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[46rem] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th scope="col" className="w-28 py-2 pr-4 font-medium">Número</th>
                <th scope="col" className="py-2 pr-4 font-medium">Cliente</th>
                <th scope="col" className="w-56 py-2 pr-4 font-medium">Situação</th>
                <th scope="col" className="w-52 py-2 pr-4 font-medium">Período</th>
                <th scope="col" className="w-32 py-2 pr-4 font-medium">Vencimento</th>
                <th scope="col" className="w-20 py-2 pr-4 text-right font-medium">Tent.</th>
                <th scope="col" className="w-32 py-2 pr-4 text-right font-medium">Total</th>
                <th scope="col" className="w-32 py-2 text-right font-medium">Em aberto</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map(invoice => (
                <tr
                  key={invoice.id}
                  className="border-b border-border last:border-0 hover:bg-surface"
                >
                  <td className="py-2 pr-4">
                    {/* The number is the link. It is what an operator has in
                        front of them when somebody calls about a bill. */}
                    <Link
                      href={`/console/invoice?id=${invoice.id}`}
                      data-numeric
                      className="font-medium text-brand-600 underline-offset-4 hover:underline"
                    >
                      {invoice.number ? `nº ${invoice.number}` : "sem número"}
                    </Link>
                  </td>
                  <td className="max-w-0 truncate py-2 pr-4 text-foreground">
                    {invoice.customer_name || (
                      <span className="text-muted-foreground">{invoice.customer_id}</span>
                    )}
                  </td>
                  <td className="py-2 pr-4"><InvoiceStatusBadge invoice={invoice}/></td>
                  <td data-numeric className="py-2 pr-4 text-muted-foreground">
                    {shortDate(invoice.period.start)} – {shortDate(invoice.period.end)}
                  </td>
                  <td data-numeric className="py-2 pr-4 text-muted-foreground">
                    {shortDate(invoice.due_date)}
                  </td>
                  {/* Attempts, because "já tentamos cobrar?" is the question
                      that decides whether a bill is a collection problem or an
                      integration one. */}
                  <td data-numeric className="py-2 pr-4 text-right text-muted-foreground">
                    {invoice.attempt_count > 0 ? invoice.attempt_count : "—"}
                  </td>
                  <td data-numeric className="py-2 pr-4 text-right text-foreground">
                    {money(invoice.total, invoice.currency)}
                  </td>
                  <td
                    data-numeric
                    className={`py-2 text-right ${
                      invoice.amount_due > 0 ? "text-foreground" : "text-muted-foreground"
                    }`}
                  >
                    {money(invoice.amount_due, invoice.currency)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {query.data?.has_more && (
        <p className="text-xs text-muted-foreground">
          Este mês tem mais faturas do que cabem em uma página.
        </p>
      )}
    </div>
  )
}

function TableSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2, 3, 4].map(i => (
        <Skeleton key={i} className="h-8 w-full"/>
      ))}
    </div>
  )
}
