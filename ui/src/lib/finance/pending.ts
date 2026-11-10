import type {Bill, PendingOccurrence} from "@/lib/api/financeTypes";

/** What waits for the person today: an open bill (a card statement included) or a recurring value to confirm. */
export type PendingItem =
  | {kind: "bill"; key: string; due: string; bill: Bill}
  | {kind: "occurrence"; key: string; due: string; item: PendingOccurrence}

/**
 * Overdue and due-today items, oldest first. Bills come with the API's own
 * bucket (the day is the space's, not the browser's); occurrences are the ones
 * whose due date has arrived. Closed card statements are bills, so they are in.
 */
export function pendingToday(bills: Bill[], occurrences: PendingOccurrence[], today: string): PendingItem[] {
  const out: PendingItem[] = [
    ...bills.filter(b => b.status === "forecast" && (b.bucket === "overdue" || b.bucket === "today"))
      .map((bill): PendingItem => ({kind: "bill", key: `b-${bill.id}`, due: bill.due_date, bill})),
    ...occurrences.filter(o => o.due <= today)
      .map((item): PendingItem => ({kind: "occurrence", key: `o-${item.recurrence_id}-${item.nominal}`, due: item.due, item})),
  ];
  return out.sort((a, b) => a.due.localeCompare(b.due));
}
