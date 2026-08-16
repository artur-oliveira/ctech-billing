import {Badge} from "@aoctech/ui"
import {AlertTriangle, CheckCircle2, Circle, Clock} from "lucide-react"

import type {Tone} from "@/lib/api/types"

/**
 * A status badge carries a glyph as well as a colour, always.
 *
 * Colour alone fails WCAG 2.2 1.4.1 and, more practically, fails the reader
 * with deuteranopia looking at a green "Paga" beside an amber "Vence em 3
 * dias" — the two most common badges on this screen, and the pair that
 * collapses first.
 */
const GLYPH = {
  neutral: Circle,
  positive: CheckCircle2,
  attention: Clock,
  urgent: AlertTriangle,
} as const

/**
 * `state` is rendered verbatim. The server already wrote the sentence a person
 * reads — "Vence em 3 dias", "Vencida há 4 dias" — and the internal status it
 * came from is not in the payload at all. Any formatting here would be the UI
 * inventing a second vocabulary for the same fact.
 */
export function StatusBadge({state, tone}: { state: string; tone: Tone }) {
  const Glyph = GLYPH[tone] ?? Circle
  return (
    <Badge tone={tone}>
      <Glyph aria-hidden/>
      {state}
    </Badge>
  )
}
