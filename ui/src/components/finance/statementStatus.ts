import {AlertCircle, CircleCheck, CircleDot, Clock, Lock} from "lucide-react";

import type {CardStatement, CardStatementStatus} from "@/lib/api/financeTypes";

export type StatementState = CardStatementStatus | "overdue"

/**
 * A statement's badge. Open is the normal state of a card, so it is calm green
 * (positive), not a warning; closed and waiting for its due date, and paid, are
 * neutral; red (urgent) is kept for the one state that needs action: closed,
 * past its due date, unpaid. Each carries a glyph, never colour alone.
 */
export const STATUS: Record<StatementState, {tone: "neutral" | "positive" | "urgent"; icon: typeof CircleDot}> = {
  open: {tone: "positive", icon: CircleDot},
  future: {tone: "neutral", icon: Clock},
  closed: {tone: "neutral", icon: Lock},
  paid: {tone: "neutral", icon: CircleCheck},
  overdue: {tone: "urgent", icon: AlertCircle},
};

/**
 * "Vencida" is not a server status: a closed statement past its due date that
 * still owes something. A zero or credit statement makes no bill, and every
 * month before a card's first reads "closed": none of them owes, so none is red.
 * A paid statement's bill makes the server say "paid".
 */
export function statementState(s: Pick<CardStatement, "status" | "due_date" | "bill_id" | "total">, today: string): StatementState {
  return s.status === "closed" && !!s.bill_id && s.total > 0 && s.due_date < today ? "overdue" : s.status;
}
