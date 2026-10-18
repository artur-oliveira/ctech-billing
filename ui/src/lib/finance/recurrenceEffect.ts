import type {ExpressionJSON, Recurrence} from "@/lib/api/financeTypes";

const DAYS_PER_MONTH = 365.25 / 12;

/**
 * How many times a rule fires in an average month. An estimate, on purpose: the
 * server owns the calendar (holidays, weekends), this only has to say "about
 * R$ 520 a month" for a weekly R$ 120. Null when the shape is unknown.
 */
export function occurrencesPerMonth(e: ExpressionJSON): number | null {
  switch (e.kind) {
    case "daily": return DAYS_PER_MONTH;
    case "business_days": return DAYS_PER_MONTH * 5 / 7;
    case "day_of_month":
    case "workday_of_month":
    case "nth_weekday_of_month": return 1;
    case "weekly": return e.every >= 1 ? DAYS_PER_MONTH / (7 * e.every) : null;
    case "yearly": return 1 / 12;
    case "difference": {
      const base = occurrencesPerMonth(e.include);
      // Whole months skipped thin the year; single dates are too few to matter.
      if (base !== null && e.exclude.kind === "months_of_year") return base * Math.max(0, 12 - new Set(e.exclude.months).size) / 12;
      return base;
    }
    default: return null;
  }
}

/** The recurrence's average monthly amount in cents (a magnitude), or null when unknown. */
export function monthlyEffect(r: Pick<Recurrence, "amount" | "expression">): number | null {
  const n = occurrencesPerMonth(r.expression);
  return n === null ? null : Math.round(r.amount * n);
}

/** True when the rule fires about once a month, so the effect equals the amount and need not be repeated. */
export function isMonthly(e: ExpressionJSON): boolean {
  return occurrencesPerMonth(e) === 1;
}
