import {Badge, type BadgeTone} from "@aoctech/ui";
import {AlertCircle, Ban, CalendarClock, CircleCheck, CircleDashed, Clock, Hourglass, type LucideIcon, Undo2} from "lucide-react";
import type {ReactNode} from "react";

import type {Bucket} from "@/lib/api/financeTypes";

/** A state's look: a tone and the glyph that says it without colour. */
export interface StatusDef {
  tone: BadgeTone
  icon: LucideIcon
}

/**
 * The one status pill of Finanças: glyph and word, never colour alone. Each
 * screen keeps a map from its own states to `StatusDef` (below, and
 * `STATUS` in statementStatus.ts) and draws it here, so a state reads the
 * same wherever it appears.
 */
export function StatusBadge({status, children, className}: {status: StatusDef; children: ReactNode; className?: string}) {
  const Icon = status.icon;
  return <Badge tone={status.tone} className={className}><Icon aria-hidden/>{children}</Badge>;
}

/** A bill's bucket: overdue is the only red. */
export const BUCKET_STATUS: Record<Bucket, StatusDef> = {
  overdue: {tone: "urgent", icon: AlertCircle},
  today: {tone: "attention", icon: Clock},
  upcoming: {tone: "neutral", icon: CalendarClock},
};

/** What a recurrence's row can flag. */
export const RECURRENCE_STATUS = {
  /** An occurrence waits for the real amount (inbox). */
  pending: {tone: "attention", icon: Hourglass},
  /** Its rule points at something archived. */
  attention: {tone: "attention", icon: AlertCircle},
  /** The amount is a forecast, not a promise. */
  estimated: {tone: "neutral", icon: CircleDashed},
} as const satisfies Record<string, StatusDef>;

/** A statement line that is, or was, a reversal. */
export const LEDGER_STATUS = {
  reversal: {tone: "neutral", icon: Undo2},
  reversed: {tone: "neutral", icon: Ban},
} as const satisfies Record<string, StatusDef>;

/** An imported line that is already settled one way or another, and one about to expire. */
export const IMPORT_STATUS = {
  /** An import with lines still waiting for a decision. */
  waiting: {tone: "attention", icon: Hourglass},
  done: {tone: "neutral", icon: CircleCheck},
  expiring: {tone: "attention", icon: Clock},
} as const satisfies Record<string, StatusDef>;

/** A plan's subscription state (Plano). */
export const PLAN_STATUS = {
  /** The plan the person is on, marked on its own card. */
  current: {tone: "neutral", icon: CircleCheck},
  pendingPayment: {tone: "attention", icon: Hourglass},
  pastDue: {tone: "urgent", icon: AlertCircle},
  trial: {tone: "neutral", icon: CircleDashed},
  ends: {tone: "neutral", icon: CalendarClock},
} as const satisfies Record<string, StatusDef>;
