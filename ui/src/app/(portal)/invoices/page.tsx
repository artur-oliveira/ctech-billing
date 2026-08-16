"use client"

import {Button, EmptyState, PageHeader, Skeleton} from "@aoctech/ui"
import {useInfiniteQuery} from "@tanstack/react-query"
import {Receipt} from "lucide-react"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {InvoiceRow} from "@/components/portal/InvoiceRow"
import {listInvoices, portalKeys} from "@/lib/api/portal"

/**
 * P2 — Faturas. A list, and nothing else.
 *
 * No filters, no saved views, no date range. Those belong to the console,
 * where an operator looks across hundreds of invoices; a customer has a dozen
 * and wants to find one by looking at it. A filter bar here would be furniture
 * that costs a tap on every visit and pays off on none.
 */
export default function InvoicesPage() {
  const query = useInfiniteQuery({
    queryKey: portalKeys.invoicePages,
    queryFn: ({pageParam}) => listInvoices(pageParam),
    initialPageParam: undefined as string | undefined,
    // Cursor pagination: the server says whether there is more and what to ask
    // for next. Never a page number — the set shifts as invoices are issued.
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
  })

  const invoices = query.data?.pages.flatMap(p => p.data) ?? []

  return (
    <div className="space-y-8">
      <PageHeader title="Faturas"/>

      {query.isPending && <ListSkeleton/>}

      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && invoices.length === 0 && (
        <EmptyState
          icon={<Receipt/>}
          title="Nenhuma fatura ainda"
          description="Assim que sua primeira cobrança for emitida, ela aparece aqui — com o que foi cobrado, quando vence e como pagar."
        />
      )}

      {invoices.length > 0 && (
        <ul className="divide-y divide-border border-y border-border">
          {invoices.map(invoice => (
            <InvoiceRow key={invoice.id} invoice={invoice}/>
          ))}
        </ul>
      )}

      {query.hasNextPage && (
        <Button
          variant="outline"
          block
          onClick={() => query.fetchNextPage()}
          disabled={query.isFetchingNextPage}
        >
          {query.isFetchingNextPage ? "Carregando…" : "Carregar mais"}
        </Button>
      )}
    </div>
  )
}

function ListSkeleton() {
  return (
    <ul className="divide-y divide-border border-y border-border" aria-busy>
      {[0, 1, 2, 3].map(i => (
        <li key={i} className="flex items-center gap-4 px-3 py-4">
          <div className="flex-1 space-y-2">
            <Skeleton className="h-4 w-2/3"/>
            <Skeleton className="h-5 w-32 rounded-full"/>
          </div>
          <Skeleton className="h-5 w-20"/>
        </li>
      ))}
    </ul>
  )
}
