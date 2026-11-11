import {useQuery} from "@tanstack/react-query";

import {financeKeys, getProjection} from "@/lib/api/finance";
import type {ProjectionCard, ProjectionMonth} from "@/lib/api/financeTypes";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";

/** Months the overview reads; every screen that shows the end of this month shares this cache entry. */
export const PROJECTION_HORIZON = 6;

/** One month of the projection, with the balance it leaves. */
export interface ProjectedMonth extends ProjectionMonth {
  result: number
  balance: number
  /** Money in and out, forecast bills plus recurrences not yet generated. */
  inflow: number
  outflow: number
  recIn: number
  recOut: number
  /** Card statements plus card recurrences: the part of `outflow` that is on a card, already posted or promised. */
  card: number
  /** One-off card spending the person chose to forecast (0 unless they set a figure), counted in `outflow` and `result`. */
  estimate: number
  /** Each card's share (empty on an older API). */
  cards: ProjectionCard[]
}

/** The projection's data, shared (one cache entry) by every block that reads the same window. */
export function useProjection(months: number) {
  const ctx = useFinanceCtx();
  return useQuery({queryKey: financeKeys.projection(ctx.mode, ctx.space, months), queryFn: () => getProjection(ctx, months)});
}

/**
 * Today's balance plus each month's result (to receive − to pay + recurrences
 * not yet generated), accumulated: what the person will have at the end of each
 * month if everything happens as forecast.
 */
export function project(start: number, data: ProjectionMonth[], budget: number | null = null): ProjectedMonth[] {
  let balance = start;
  return data.map(m => {
    // Nothing is guessed: with no figure from the person the projection holds only what is
    // registered. A figure of their own (what the cards take beyond plan each month, all cards
    // together) counts for what the statements do not hold of it yet.
    const cards = m.by_card ?? [];
    const estimate = budget !== null && m.by_card
      ? Math.max(budget - cards.reduce((a, c) => a + c.unplanned, 0), 0)
      : 0;
    const result = m.receivable - m.payable + m.virtual - estimate;
    balance += result;
    const recIn = m.virtual_receivable ?? Math.max(m.virtual, 0);
    const recOut = m.virtual_payable ?? Math.max(-m.virtual, 0);
    const card = (m.card_payable ?? 0) + (m.virtual_card_payable ?? 0);
    return {...m, result, balance, recIn, recOut, card, estimate, cards, inflow: m.receivable + recIn, outflow: m.payable + recOut + estimate};
  });
}

/** The projection horizons, in months: 3m, 6m, then 1 to 5 years (the API reaches 60). */
export const HORIZONS = [3, 6, 12, 24, 36, 48, 60] as const;

/** Most bars the chart draws: a phone has no room for more than six. */
export const maxBars = (narrow: boolean) => (narrow ? 6 : 12);

/** One or more consecutive months folded into a single bar or row. */
export interface ProjectedPeriod extends ProjectedMonth {
  /** The period's last month (YYYY-MM); equal to `month` for a single month. */
  last: string
  /** How many months it spans. */
  count: number
}

/** Months per bar, smallest first: months, bi-months, quarters, 4-month spans, semesters, years. */
const SPANS = [1, 2, 3, 4, 6, 12];

/**
 * Folds the months into at most `max` periods of equal length (the last may be
 * shorter), so a 5-year horizon is five bars, not sixty. Flows sum; the balance
 * is the last month's, so the ending balance is exactly the unaggregated one.
 */
export function aggregate(data: ProjectedMonth[], max: number): ProjectedPeriod[] {
  const span = SPANS.find(s => Math.ceil(data.length / s) <= max) ?? 12;
  const out: ProjectedPeriod[] = [];
  for (let i = 0; i < data.length; i += span) {
    const g = data.slice(i, i + span);
    const end = g[g.length - 1];
    const sum = (f: (m: ProjectedMonth) => number) => g.reduce((a, m) => a + f(m), 0);
    out.push({
      ...g[0], last: end.month, count: g.length, balance: end.balance,
      receivable: sum(m => m.receivable), payable: sum(m => m.payable), virtual: sum(m => m.virtual),
      result: sum(m => m.result), inflow: sum(m => m.inflow), outflow: sum(m => m.outflow),
      recIn: sum(m => m.recIn), recOut: sum(m => m.recOut), card: sum(m => m.card), estimate: sum(m => m.estimate),
      cards: mergeCards(g),
    });
  }
  return out;
}

/** The cards of several months as one: the money sums. */
function mergeCards(months: ProjectedMonth[]): ProjectionCard[] {
  const by = new Map<string, ProjectionCard>();
  for (const m of months) {
    for (const c of m.cards) {
      const a = by.get(c.card_id);
      by.set(c.card_id, a ? {...c, posted: a.posted + c.posted, expected: a.expected + c.expected, unplanned: a.unplanned + c.unplanned} : c);
    }
  }
  return [...by.values()];
}
