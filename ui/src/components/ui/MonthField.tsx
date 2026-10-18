"use client";

import {cn} from "@aoctech/ui";
import {Popover} from "@base-ui/react/popover";
import {CalendarDays, ChevronLeft, ChevronRight} from "lucide-react";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {normalizeLocale} from "@/lib/locale";

const YM = /^\d{4}-\d{2}$/;

interface MonthFieldProps {
  id: string
  /** A civil month, `YYYY-MM`, or "" for none. */
  value: string
  onValueChange: (ym: string) => void
  min?: string
  max?: string
  placeholder?: string
  disabled?: boolean
  className?: string
  invalid?: boolean
}

/**
 * Month picker for `YYYY-MM`, the DateField's sibling. A native
 * `<input type="month">` renders in the browser's locale whatever `lang` says
 * (it showed English under a pt-BR UI), so this draws its own: a trigger and a
 * popover with a year header and twelve month buttons, names from Intl in the
 * app's language. Labelled by a Field's `htmlFor` through `id`.
 */
export function MonthField({id, value, onValueChange, min, max, placeholder, disabled, className, invalid}: MonthFieldProps) {
  const {t, i18n} = useTranslation();
  const locale = normalizeLocale(i18n.language);
  const selected = YM.test(value) ? value : "";
  const [open, setOpen] = useState(false);
  const [year, setYear] = useState(() => Number((selected || max || min || todayYm()).slice(0, 4)));
  const names = monthNames(locale);
  const minYear = YM.test(min ?? "") ? Number(min!.slice(0, 4)) : -Infinity;
  const maxYear = YM.test(max ?? "") ? Number(max!.slice(0, 4)) : Infinity;
  const key = (m: number) => `${year}-${String(m).padStart(2, "0")}`;
  const blocked = (m: number) => (!!min && YM.test(min) && key(m) < min) || (!!max && YM.test(max) && key(m) > max);

  return (
    <Popover.Root open={open} onOpenChange={o => { setOpen(o); if (o) setYear(Number((selected || max || min || todayYm()).slice(0, 4))); }}>
      <Popover.Trigger
        id={id}
        disabled={disabled}
        aria-invalid={invalid || undefined}
        data-slot="date-picker-trigger"
        className={cn(
          "flex h-11 w-full min-w-44 items-center justify-between gap-2 rounded-lg border border-border bg-background px-3 text-left text-sm text-foreground outline-none transition-[border-color,box-shadow] duration-150 hover:bg-surface focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/35 disabled:cursor-not-allowed disabled:opacity-50 in-data-[density=compact]:h-8 in-data-[density=compact]:px-2.5",
          !selected && "text-muted-foreground",
          invalid && "border-danger ring-3 ring-danger/20",
          className,
        )}
      >
        <span className="truncate">{selected ? monthTitle(selected, locale) : placeholder ?? t("auth.month.placeholder")}</span>
        <CalendarDays aria-hidden className="size-4 shrink-0 text-muted-foreground"/>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner side="bottom" align="start" sideOffset={8} className="z-50">
          <Popover.Popup aria-label={t("auth.month.pick")} className="w-72 max-w-[calc(100vw-2rem)] rounded-xl border border-border bg-background p-3 shadow-modal outline-none">
            <div className="mb-2 flex items-center justify-between">
              <button type="button" aria-label={t("auth.month.prevYear")} disabled={year <= minYear} onClick={() => setYear(year - 1)} className={navBtn}><ChevronLeft aria-hidden className="size-4"/></button>
              <span aria-live="polite" className="text-sm font-medium tabular-nums">{year}</span>
              <button type="button" aria-label={t("auth.month.nextYear")} disabled={year >= maxYear} onClick={() => setYear(year + 1)} className={navBtn}><ChevronRight aria-hidden className="size-4"/></button>
            </div>
            <div role="group" aria-label={String(year)} className="grid grid-cols-3 gap-1">
              {names.map((name, i) => {
                const m = i + 1;
                const on = key(m) === selected;
                return (
                  <button key={m} type="button" aria-pressed={on} disabled={blocked(m)} aria-label={`${name} ${year}`}
                    onClick={() => { onValueChange(key(m)); setOpen(false); }}
                    className={cn("min-h-11 rounded-md px-2 text-sm capitalize transition-colors disabled:cursor-not-allowed disabled:opacity-40", on ? "bg-brand-600 font-medium text-white" : "text-foreground hover:bg-surface")}>
                    {name}
                  </button>
                );
              })}
            </div>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

const navBtn = "inline-flex size-11 items-center justify-center rounded-md text-foreground hover:bg-surface disabled:cursor-not-allowed disabled:opacity-40";

function todayYm(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
}

/** Short month names ("jan.", "Jan") in the given language, January first. */
export function monthNames(locale: string): string[] {
  const f = new Intl.DateTimeFormat(locale, {month: "short"});
  return Array.from({length: 12}, (_, i) => f.format(new Date(2026, i, 1, 12)));
}

/** "março de 2026" / "March 2026" from `YYYY-MM`. */
export function monthTitle(ym: string, locale: string): string {
  const [y, m] = ym.split("-").map(Number);
  return new Intl.DateTimeFormat(locale, {month: "long", year: "numeric"}).format(new Date(y, m - 1, 1, 12));
}
