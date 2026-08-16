import {ChevronRight} from "lucide-react"
import Link from "next/link"

import {Money} from "@/components/portal/Money"
import {StatusBadge} from "@/components/portal/StatusBadge"
import type {Invoice} from "@/lib/api/types"
import {shortDate} from "@/lib/format"

/**
 * One invoice in a list. The whole row is the link, so the tap target is the
 * row rather than a "ver" affordance the reader has to aim at.
 *
 * The amount shown is `amount_due` while anything is owed and the total once
 * it is settled: a paid invoice reading "R$ 0,00" is technically what is left
 * to pay and is not what anybody means by "how much was this".
 */
export function InvoiceRow({invoice}: { invoice: Invoice }) {
  const amount = invoice.amount_due > 0 ? invoice.amount_due : invoice.total

  return (
    <li>
      <Link
        href={`/invoice?id=${invoice.id}`}
        className="group flex items-center gap-4 rounded-xl px-3 py-4 transition-colors hover:bg-surface"
      >
        <div className="min-w-0 flex-1 space-y-1.5">
          <p className="truncate text-sm font-medium text-foreground">{invoice.description}</p>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <StatusBadge state={invoice.state} tone={invoice.tone}/>
            <span data-numeric className="text-xs text-muted-foreground">
              {shortDate(invoice.due_date)}
            </span>
          </div>
        </div>
        <Money cents={amount} currency={invoice.currency}/>
        <ChevronRight
          aria-hidden
          className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none"
        />
      </Link>
    </li>
  )
}
