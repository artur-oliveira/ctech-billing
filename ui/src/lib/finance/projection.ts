import {useQuery} from "@tanstack/react-query";

import {financeKeys, getProjection} from "@/lib/api/finance";
import type {ProjectionMonth} from "@/lib/api/financeTypes";
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
export function project(start: number, data: ProjectionMonth[]): ProjectedMonth[] {
  let balance = start;
  return data.map(m => {
    const result = m.receivable - m.payable + m.virtual;
    balance += result;
    const recIn = m.virtual_receivable ?? Math.max(m.virtual, 0);
    const recOut = m.virtual_payable ?? Math.max(-m.virtual, 0);
    return {...m, result, balance, recIn, recOut, inflow: m.receivable + recIn, outflow: m.payable + recOut};
  });
}
