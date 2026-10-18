"use client";

import {useQuery} from "@tanstack/react-query";

import {CategoryBreakdown} from "@/components/finance/CategoryBreakdown";
import {financeKeys, getCategoryMonth, listAccounts} from "@/lib/api/finance";
import {nameOf} from "@/lib/finance/accountName";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";

/** The category breakdown of `month` (`YYYY-MM`), fetched: shared by Reports and the Overview. */
export function CategoryMonthPanel({month}: {month: string}) {
  const ctx = useFinanceCtx();
  const q = useQuery({queryKey: financeKeys.categoryMonth(ctx.mode, ctx.space, month), queryFn: () => getCategoryMonth(ctx, month)});
  const accounts = useQuery({queryKey: financeKeys.accounts(ctx.mode, ctx.space), queryFn: () => listAccounts(ctx)});
  const names = accounts.data?.data ?? [];
  return (
    <CategoryBreakdown data={q.data} loading={q.isLoading || accounts.isLoading} error={q.error ?? accounts.error}
      onRetry={() => { void q.refetch(); void accounts.refetch(); }} nameOf={id => nameOf(names, id)}/>
  );
}
