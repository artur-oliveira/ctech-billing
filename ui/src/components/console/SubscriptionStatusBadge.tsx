import {Badge} from "@aoctech/ui"
import {AlertTriangle, Ban, CheckCircle2, Circle, Clock, PauseCircle} from "lucide-react"

import type {SubscriptionStatus} from "@/lib/api/consoleTypes"

/**
 * The subscription's internal status, spelled out — the console's vocabulary,
 * not the portal's phrase.
 *
 * INCOMPLETE is the one worth naming carefully: it means a paid plan whose
 * first invoice has not been paid, so the service was never granted. "Aguardando
 * primeiro pagamento" is what an operator needs to read, because the question
 * they are answering is why a customer says nothing works.
 */
const LABEL: Record<SubscriptionStatus, string> = {
  INCOMPLETE: "Aguardando 1º pagamento",
  TRIALING: "Em teste",
  ACTIVE: "Ativa",
  PAST_DUE: "Em atraso",
  PAUSED: "Pausada",
  CANCELED: "Encerrada",
}

const TONE = {
  INCOMPLETE: "attention",
  TRIALING: "positive",
  ACTIVE: "positive",
  PAST_DUE: "urgent",
  PAUSED: "neutral",
  CANCELED: "neutral",
} as const

const GLYPH = {
  INCOMPLETE: Clock,
  TRIALING: Circle,
  ACTIVE: CheckCircle2,
  PAST_DUE: AlertTriangle,
  PAUSED: PauseCircle,
  CANCELED: Ban,
} as const

export function SubscriptionStatusBadge({
  status,
  endingAtPeriodEnd,
}: { status: SubscriptionStatus; endingAtPeriodEnd?: boolean }) {
  const Glyph = GLYPH[status] ?? Circle
  return (
    <span className="inline-flex items-center gap-1.5">
      <Badge tone={TONE[status]}>
        <Glyph aria-hidden/>
        {LABEL[status] ?? status}
      </Badge>
      {/* A second badge rather than a replaced status: it is still ACTIVE, and
          what changed is that it will not renew. Collapsing the two loses which
          of them is true today. */}
      {endingAtPeriodEnd && status !== "CANCELED" && (
        <Badge tone="attention">
          <Clock aria-hidden/>
          Encerra no fim do período
        </Badge>
      )}
    </span>
  )
}
