import {currentLocale, t} from "@/lib/i18n";

/**
 * Today as a civil `YYYY-MM-DD` in the reader's clock, built from the local
 * date parts — never `toISOString()`, which is UTC and is yesterday every
 * evening in São Paulo.
 */
export function todayIso(now: Date = new Date()): string {
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

/** "março de 2026" / "March 2026" for a civil date's month. */
export function monthLabel(iso: string): string {
  const [y, m] = iso.split("-").map(Number);
  return new Intl.DateTimeFormat(currentLocale(), {month: "long", year: "numeric"}).format(new Date(y, m - 1, 1));
}

/** "Mar/26" — a month in a table header or an axis, from `YYYY-MM`. */
export function monthShort(ym: string): string {
  const [y, m] = ym.split("-");
  return `${t(`finance.monthShort.${Number(m)}`)}/${y.slice(2)}`;
}

/** `iso` moved by n years, Feb 29 falling back to Feb 28 — civil arithmetic only. */
export function addYearsIso(iso: string, n: number): string {
  const [y, m, d] = iso.split("-").map(Number);
  const year = y + n;
  const last = new Date(year, m, 0).getDate();
  return `${year}-${String(m).padStart(2, "0")}-${String(Math.min(d, last)).padStart(2, "0")}`;
}

/** `iso` moved by n days (negative goes back), civil arithmetic only: no UTC read, no DST. */
export function addDaysIso(iso: string, n: number): string {
  const [y, m, d] = iso.split("-").map(Number);
  return todayIso(new Date(y, m - 1, d + n, 12));
}

/** "14:05" for an instant, on the São Paulo clock the whole product keeps, whatever the browser's zone. */
export function timeHm(instant: string): string {
  return new Intl.DateTimeFormat(currentLocale(), {hour: "2-digit", minute: "2-digit", timeZone: "America/Sao_Paulo"}).format(new Date(instant));
}

/** Whole civil days from `from` to `to` (negative when `to` is earlier); no clock, no DST. */
export function daysBetween(from: string, to: string): number {
  const [fy, fm, fd] = from.split("-").map(Number);
  const [ty, tm, td] = to.split("-").map(Number);
  return Math.round((Date.UTC(ty, tm - 1, td) - Date.UTC(fy, fm - 1, fd)) / 86_400_000);
}

/**
 * A due date as a person says it: "venceu há 5 dias", "vence hoje", "vence
 * amanhã", "vence em 3 dias". Null beyond 30 days either way, where the date
 * itself says more than a count.
 */
export function dueLabel(due: string, today: string): string | null {
  const d = daysBetween(today, due);
  if (Math.abs(d) > 30) return null;
  if (d < 0) return t("bills.row.overdueBy", {count: -d});
  if (d === 0) return t("bills.row.dueToday");
  if (d === 1) return t("bills.row.dueTomorrow");
  return t("bills.row.dueIn", {count: d});
}
