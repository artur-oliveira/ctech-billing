import {Badge} from "@aoctech/ui"
import {AlertTriangle, CheckCircle2, Circle, Clock} from "lucide-react"

import {useTranslation} from "react-i18next"

import type {Tone} from "@/lib/api/types"
import {shortDate} from "@/lib/format"

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
 * `state` is an enum code from the API ("overdue", "due_soon", "active"...);
 * the words are the catalog's. `days` is days until due (negative once overdue)
 * and `dueDate` feeds "upcoming", so no sentence is ever assembled server-side.
 */
export function StatusBadge({state, tone, days, dueDate}: {
  state: string
  tone: Tone
  days?: number
  dueDate?: string
}) {
  const {t} = useTranslation()
  const Glyph = GLYPH[tone] ?? Circle
  return (
    <Badge tone={tone}>
      <Glyph aria-hidden/>
      {t(`portal.status.${state}`, {
        count: Math.abs(days ?? 0),
        date: dueDate ? shortDate(dueDate) : "",
        defaultValue: state,
      })}
    </Badge>
  )
}
