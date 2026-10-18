import type {CategoryAmounts, CategoryMonth} from "@/lib/api/financeTypes";

export type SideKey = "income" | "expense"

/** One side of the month with its rows, in the order they are read: income, then expense. */
export interface BreakdownSide {
  key: SideKey
  totals: CategoryAmounts
  rows: CategoryAmounts[]
}

export function sidesOf(m: CategoryMonth): BreakdownSide[] {
  return [
    {key: "income", totals: m.income.totals, rows: m.income.categories},
    {key: "expense", totals: m.expense.totals, rows: m.expense.categories},
  ];
}

/** Whether the month holds nothing at all, so the screen can say so once instead of two empty tables. */
export function isEmptyMonth(m: CategoryMonth): boolean {
  return m.income.categories.length === 0 && m.expense.categories.length === 0;
}

/** What is already in the books: realised plus scheduled (open bills). The rest, `forecast`, is a rule's promise. */
export const booked = (a: Pick<CategoryAmounts, "realised" | "scheduled">): number => a.realised + a.scheduled;

/** `part` of `whole`, 0..1; 0 for a negative part (a refund) or an empty whole. */
export function share(part: number, whole: number): number {
  return whole > 0 && part > 0 ? Math.min(1, part / whole) : 0;
}

/** Income minus expense, column by column: what the month is expected to leave. */
export function netOf(m: CategoryMonth): Omit<CategoryAmounts, "category_id"> {
  const i = m.income.totals, e = m.expense.totals;
  return {realised: i.realised - e.realised, scheduled: i.scheduled - e.scheduled, forecast: i.forecast - e.forecast, total: i.total - e.total};
}
