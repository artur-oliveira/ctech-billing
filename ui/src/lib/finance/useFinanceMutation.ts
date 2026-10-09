"use client"

import {type QueryKey, useMutation, useQueryClient} from "@tanstack/react-query"

import {type FinanceCtx} from "@/lib/api/finance"
import {useIntent} from "@/lib/api/idempotency"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"

/**
 * Every finance write goes through here, so no dialog re-implements:
 *  - one Idempotency-Key per intent, kept across retries, renewed on success;
 *  - invalidating only the CURRENT (mode, space)'s keys, never another's.
 * The 404 space-not-found fallback is in finance.ts and needs nothing here.
 */
export function useFinanceMutation<V, R>(
  run: (ctx: FinanceCtx, vars: V, idempotencyKey: string) => Promise<R>,
  invalidate: (ctx: FinanceCtx) => QueryKey[],
  onSuccess?: (result: R) => void,
  onError?: (error: Error) => void,
) {
  const ctx = useFinanceCtx()
  const client = useQueryClient()
  const intent = useIntent()
  const mutation = useMutation({
    mutationFn: (vars: V) => run(ctx, vars, intent.key()),
    onSuccess: result => {
      intent.done()
      for (const key of invalidate(ctx)) void client.invalidateQueries({queryKey: key})
      onSuccess?.(result)
    },
    onError,
  })
  // newIntent: the person changed WHAT they are submitting (another file), so a
  // failed attempt's key must not be reused for it.
  return Object.assign(mutation, {newIntent: intent.done})
}
