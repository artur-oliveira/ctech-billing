"use client";

import {useTranslation} from "react-i18next";

import {money, signedMoney} from "@/lib/format";

interface StatementTotalProps {
  /** What the statement already holds (its `total`). */
  posted: number
  /** What the card's recurrences will still add; 0 or undefined when none. */
  forecast?: number
  /** Part of `forecast` is an expected amount, awaiting confirmation. */
  estimated?: boolean
  /** The forecast is still being read, or could not be: say so instead of showing the posted part as the whole month. */
  forecastState?: "loading" | "error"
  /** Headline size: `lg` for the open statement's own header, `base` in a card. */
  size?: "base" | "lg"
  className?: string
}

/**
 * A statement's month as one figure: posted plus what recurrences will still add,
 * with the two parts beneath it ("já lançado R$ 100,00 · recorrências previstas
 * R$ 340,00", "estimado" when some of it is an expected amount). Everywhere a
 * statement is drawn uses this, so the same statement never shows two totals.
 */
export function StatementTotal({posted, forecast = 0, estimated, forecastState, size = "base", className}: StatementTotalProps) {
  const {t} = useTranslation();
  return (
    <span className={`flex min-w-0 flex-col gap-0.5 ${className ?? ""}`}>
      <span data-numeric className={`${size === "lg" ? "text-lg" : "text-base"} font-semibold tabular-nums text-foreground [overflow-wrap:anywhere]`}>
        {signedMoney(posted + forecast)}
      </span>
      {posted + forecast < 0 && <span className="text-xs text-muted-foreground">{t("finance.cards.total.credit")}</span>}
      {forecastState && (
        <span className="text-xs text-muted-foreground">{t(forecastState === "loading" ? "finance.cards.total.forecastLoading" : "finance.cards.total.forecastError")}</span>
      )}
      {forecast > 0 && (
        <span data-numeric className="text-balance text-xs tabular-nums text-muted-foreground">
          {t("finance.cards.total.posted", {amount: signedMoney(posted)})}
          {" · "}
          {t(estimated ? "finance.cards.total.forecastEstimated" : "finance.cards.total.forecast", {amount: money(forecast)})}
        </span>
      )}
    </span>
  );
}
