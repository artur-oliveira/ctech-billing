import type {StatementDirection, StatementPage, StatementSummary} from "@/lib/api/financeTypes";

/** Statements fetched per step beyond the loaded edge (spec D5). */
export const PAGE_SIZE = 3;
/** Statements each side of the open one on opening. */
export const AROUND = 3;

export interface PageParam {from: string; direction: StatementDirection; count: number}
/** A page as the API answered it, with the request that asked for it. */
export interface TimelinePage extends StatementPage {param: PageParam}

export function shiftMonth(ym: string, n: number): string {
  const [y, m] = ym.split("-").map(Number);
  const i = y * 12 + (m - 1) + n;
  return `${Math.floor(i / 12)}-${String((i % 12) + 1).padStart(2, "0")}`;
}

/** One request opens the timeline: forward from 3 before the open month, 7 statements.
 *  The API starts a forward page at the card's first month when `from` is earlier. */
export const initialParam = (open: string): PageParam => ({from: shiftMonth(open, -AROUND), direction: "forward", count: 2 * AROUND + 1});

/** Next pages are always forward, after the last loaded month. */
export function nextParam(last: TimelinePage): PageParam | undefined {
  const end = last.data.at(-1);
  if (last.param.direction !== "forward" || !last.has_more || !end) return undefined;
  return {from: shiftMonth(end.month, 1), direction: "forward", count: PAGE_SIZE};
}

/** Previous pages are always backward, before the first loaded month. The opening
 *  page is forward and says nothing about the past: ask once, the answer's
 *  has_more decides after that. A backward page lists newest first. */
export function previousParam(first: TimelinePage): PageParam | undefined {
  if (first.param.direction === "forward") {
    return {from: shiftMonth(first.data[0]?.month ?? first.param.from, -1), direction: "backward", count: PAGE_SIZE};
  }
  const earliest = first.data.at(-1);
  return first.has_more && earliest ? {from: shiftMonth(earliest.month, -1), direction: "backward", count: PAGE_SIZE} : undefined;
}

/** Every loaded statement, oldest first, each month once. */
export function flatten(pages: TimelinePage[]): StatementSummary[] {
  const byMonth = new Map<string, StatementSummary>();
  for (const p of pages) for (const s of p.data) byMonth.set(s.month, s);
  return [...byMonth.values()].sort((a, b) => a.month.localeCompare(b.month));
}

function startIndex(months: string[], start: string, size: number): number {
  const i = months.findIndex(m => m >= start);
  const last = Math.max(0, months.length - size);
  return i < 0 ? last : Math.min(i, last);
}

/** The visible slice [from, to) for a window starting at month `start`. Anchoring
 *  by month, not index, keeps the same statements on screen when older ones load. */
export function windowAt(months: string[], start: string, size: number): {from: number; to: number} {
  const from = startIndex(months, start, size);
  return {from, to: Math.min(months.length, from + size)};
}

/** The window's start a page earlier or later, within what is loaded. */
export function stepWindow(months: string[], start: string, size: number, dir: -1 | 1): string {
  if (months.length === 0) return start;
  const i = startIndex(months, start, size) + dir * PAGE_SIZE;
  return months[Math.max(0, Math.min(i, Math.max(0, months.length - size)))];
}

/** The window's start moved just enough for `selected` to be visible. */
export function keepVisible(months: string[], start: string, size: number, selected: string): string {
  const {from, to} = windowAt(months, start, size);
  const i = months.indexOf(selected);
  if (i < 0 || (i >= from && i < to)) return months[from] ?? start;
  return i < from ? months[i] : months[Math.max(0, i - size + 1)];
}

/** How many loaded statements lie outside the window on each side. */
export function edges(months: string[], start: string, size: number): {before: number; after: number} {
  const {from, to} = windowAt(months, start, size);
  return {before: from, after: months.length - to};
}
