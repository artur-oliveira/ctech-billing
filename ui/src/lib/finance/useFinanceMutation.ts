"use client";

import {type QueryKey, useMutation, useQueryClient} from "@tanstack/react-query";
import {toast} from "sonner";

import {type FinanceCtx} from "@/lib/api/finance";
import {useIntent} from "@/lib/api/idempotency";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";

/**
 * Every finance write goes through here, so no dialog re-implements:
 *  - one Idempotency-Key per intent, kept across retries, renewed on success;
 *  - invalidating only the CURRENT (mode, space)'s keys, never another's.
 *
 * `invalidate` also sees the result (none on a failure) and what was submitted, so a write that is
 * only the first half of an intent can leave the cache alone: refetching
 * between the halves paints a state the person never asked for (a bill
 * created already paid flashed in the open list until its settle landed).
 * The 404 space-not-found fallback is in finance.ts and needs nothing here.
 */
/**
 * A write that can be taken back for a few seconds: after it lands, a toast
 * offers `actionLabel`; pressing it runs `run` (with its own Idempotency-Key),
 * refreshes the same keys and says `done`, or `failed`.
 */
export interface UndoOptions<V, R> {
  message: string
  actionLabel: string
  done: string
  failed: string
  run: (ctx: FinanceCtx, vars: V, result: R, idempotencyKey: string) => Promise<unknown>
  /** Milliseconds the offer stays. */
  duration?: number
}

export function useFinanceMutation<V, R>(
  run: (ctx: FinanceCtx, vars: V, idempotencyKey: string) => Promise<R>,
  invalidate: (ctx: FinanceCtx, result: R | undefined, vars: V) => QueryKey[],
  onSuccess?: (result: R, vars: V) => void,
  onError?: (error: Error) => void,
  options: {
    /**
     * Refresh the same keys on a failure too: for the second half of an intent
     * whose first half already landed (a bill created, its settle failed), the
     * cache must show what is now true.
     */
    invalidateOnError?: boolean
    /** Offer to take the write back (see UndoOptions). */
    undo?: UndoOptions<V, R>
  } = {},
) {
  const ctx = useFinanceCtx();
  const client = useQueryClient();
  const intent = useIntent();
  const mutation = useMutation({
    mutationFn: (vars: V) => run(ctx, vars, intent.key()),
    onSuccess: (result, vars) => {
      intent.done();
      for (const key of invalidate(ctx, result, vars)) void client.invalidateQueries({queryKey: key});
      onSuccess?.(result, vars);
      const undo = options.undo;
      if (undo) {
        toast.success(undo.message, {
          duration: undo.duration ?? 10000,
          action: {
            label: undo.actionLabel,
            onClick: () => {
              void undo.run(ctx, vars, result, crypto.randomUUID()).then(async () => {
                await Promise.all(invalidate(ctx, result, vars).map(queryKey => client.invalidateQueries({queryKey})));
                toast.success(undo.done);
              }).catch(() => toast.error(undo.failed));
            },
          },
        });
      }
    },
    onError: (error, vars) => {
      if (options.invalidateOnError) for (const key of invalidate(ctx, undefined, vars)) void client.invalidateQueries({queryKey: key});
      onError?.(error);
    },
  });
  // newIntent: the person changed WHAT they are submitting (another file), so a
  // failed attempt's key must not be reused for it.
  return Object.assign(mutation, {newIntent: intent.done});
}
