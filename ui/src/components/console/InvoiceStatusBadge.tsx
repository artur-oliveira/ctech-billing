import {Badge} from "@aoctech/ui"
import {AlertTriangle, Ban, CheckCircle2, Circle, FileText} from "lucide-react"

import {useTranslation} from "react-i18next"

import type {ConsoleInvoice} from "@/lib/api/consoleTypes"

/**
 * The internal status, spelled out — the opposite of the portal's badge, and
 * deliberately so. An operator asks "what state is this record in" and already
 * knows what UNCOLLECTIBLE means; translating it into "pendente de acordo" here
 * would hide the vocabulary the API, the audit trail and the support
 * conversation all use.
 *
 * Overdue is not one of them. It is derived on read and not stored, which is
 * why it rides alongside as a second badge rather than replacing OPEN — an
 * invoice does not stop being open by being late.
 */
const TONE = {
  DRAFT: "neutral",
  OPEN: "attention",
  PAID: "positive",
  VOID: "neutral",
  UNCOLLECTIBLE: "urgent",
} as const

const GLYPH = {
  DRAFT: FileText,
  OPEN: Circle,
  PAID: CheckCircle2,
  VOID: Ban,
  UNCOLLECTIBLE: AlertTriangle,
} as const

export function InvoiceStatusBadge({invoice}: { invoice: ConsoleInvoice }) {
  const {t} = useTranslation()
  const Glyph = GLYPH[invoice.status] ?? Circle
  return (
    <span className="inline-flex items-center gap-1.5">
      <Badge tone={TONE[invoice.status]}>
        <Glyph aria-hidden/>
        {t(`console.invoiceStatus.${invoice.status}`, {defaultValue: invoice.status})}
      </Badge>
      {invoice.overdue && invoice.status === "OPEN" && (
        <Badge tone="urgent">
          <AlertTriangle aria-hidden/>
          {t("console.invoiceStatus.overdue")}
        </Badge>
      )}
    </span>
  )
}
