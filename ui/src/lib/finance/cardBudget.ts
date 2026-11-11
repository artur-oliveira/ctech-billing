"use client";

import {useCallback, useSyncExternalStore} from "react";

import {spaceHeader} from "@/lib/console/space";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";

/** Cents a person may set as their cards' monthly unplanned spending: a sanity bound, not a rule. */
const MAX_BUDGET = 1_000_000_00;

const CHANGED = "ctech-card-budget";

/** What a blocked storage could not keep: the choice lasts until the page closes. */
const memory = new Map<string, string>();

function read(key: string): number | null {
  let raw: string | null = memory.get(key) ?? null;
  try {
    raw = window.localStorage.getItem(key) ?? raw;
  } catch { /* blocked storage: the in-memory value stands */ }
  const n = raw === null ? NaN : Number(raw);
  return Number.isInteger(n) && n >= 0 && n <= MAX_BUDGET ? n : null;
}

function subscribe(onChange: () => void) {
  window.addEventListener("storage", onChange);
  window.addEventListener(CHANGED, onChange);
  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener(CHANGED, onChange);
  };
}

/**
 * The person's own monthly budget for unplanned card spending, kept in this
 * browser per mode and space (it is an assumption for looking ahead, not a record;
 * another browser starts on the automatic estimate). Null is "use the automatic
 * one". Storage can be blocked or hold anything: every read is checked and the
 * screen works without it.
 */
export function useCardBudget(): [number | null, (cents: number | null) => void] {
  const ctx = useFinanceCtx();
  const key = `ctech-billing-card-budget:${ctx.mode}:${spaceHeader(ctx.space)}`;
  const budget = useSyncExternalStore(subscribe, () => read(key), () => null);

  const set = useCallback((cents: number | null) => {
    if (cents === null) memory.delete(key);
    else memory.set(key, String(cents));
    try {
      if (cents === null) window.localStorage.removeItem(key);
      else window.localStorage.setItem(key, String(cents));
    } catch { /* blocked storage: kept in memory above */ }
    window.dispatchEvent(new Event(CHANGED));
  }, [key]);

  return [budget, set];
}
