"use client";

import {useQueryClient} from "@tanstack/react-query";
import {useEffect} from "react";
import {useTranslation} from "react-i18next";

import {financeKeys} from "@/lib/api/finance";
import {messageFor, problemCode, statusOf} from "@/lib/api/client";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";

/**
 * The words for a failed write. A bare 409 means the row changed under the person: the
 * whole space is refetched (so the form's next try sees what is true) and the
 * message says so. `fe.general` (from useFieldErrors) carries the validation
 * leftovers no control claimed; without `fe` the error's own message is used.
 */
export function useErrorText(error: unknown, fe?: {general?: string}): string | undefined {
  const {t} = useTranslation();
  const client = useQueryClient();
  const ctx = useFinanceCtx();
  // A 409 that names its own reason (opening_balance_exists) says it in its own words.
  const conflict = statusOf(error) === 409 && (!problemCode(error) || problemCode(error) === "conflict");
  useEffect(() => {
    if (conflict) void client.invalidateQueries({queryKey: financeKeys.all(ctx.mode, ctx.space)});
  }, [conflict, client, ctx.mode, ctx.space]);
  if (!error) return fe?.general;
  return conflict ? t("finance.conflict") : fe ? fe.general : messageFor(error);
}

/** The text alone, for a container that draws its own alert (InlineConfirm). */
export function FormErrorText({error}: {error: unknown}) {
  return <>{useErrorText(error)}</>;
}

/** One alert line under a form's buttons: the mutation's error and/or the field leftovers. */
export function FormError({error, fe, className = "w-full"}: {error?: unknown; fe?: {general?: string}; className?: string}) {
  const text = useErrorText(error, fe);
  if (!text) return null;
  return <p role="alert" className={`${className} text-sm text-danger`}>{text}</p>;
}
