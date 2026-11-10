"use client";

import {Button, EmptyState, PageHeader, Segmented, Skeleton} from "@aoctech/ui";
import {useInfiniteQuery} from "@tanstack/react-query";
import {ChevronLeft, ChevronRight, FileText} from "lucide-react";
import Link from "next/link";
import {useSearchParams} from "next/navigation";
import {Suspense, useState} from "react";
import {useTranslation} from "react-i18next";

import {InvoiceStatusBadge} from "@/components/console/InvoiceStatusBadge";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {consoleKeys, listConsoleInvoices} from "@/lib/api/console";
import type {ConsoleInvoiceRow} from "@/lib/api/consoleTypes";
import {useConsoleScope} from "@/lib/console/scope";
import {compactDate, compactPeriod, money, shortDate} from "@/lib/format";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";
import {NARROW, useMediaQuery} from "@/lib/hooks/useMediaQuery";
import {currentLocale} from "@/lib/i18n";

/**
 * C2 — the invoice list, and the screen an operator lives in.
 *
 * It is a real table, not the portal's stack of rows: the whole job here is
 * comparing a column down the page — who owes what, due when — and a card per
 * invoice makes that impossible by construction. On a phone it becomes a list
 * (DESIGN.md "On a phone"): number and status, the customer, then due date and
 * what is still owed.
 *
 * Paged by **month** rather than by an open-ended cursor, because that is the
 * partition the API indexes (`ListByMonth`) and, more to the point, because
 * "faturas de agosto" is how an operator asks. The cursor inside the month is
 * followed by "Mostrar mais" for the tenant that outgrows one page.
 */
export default function ConsoleInvoicesPage() {
  return (
    <Suspense fallback={<TableSkeleton/>}>
      <Invoices/>
    </Suspense>
  );
}

type Filter = "all" | "overdue" | "upcoming" | "paid";
const FILTERS: Filter[] = ["all", "overdue", "upcoming", "paid"];

/** A lens over the pages already loaded: the month is the server's partition,
 *  the status only narrows it. */
const MATCH: Record<Filter, (invoice: ConsoleInvoiceRow) => boolean> = {
  all: () => true,
  overdue: i => i.status === "OPEN" && i.overdue,
  upcoming: i => i.status === "OPEN" && !i.overdue,
  paid: i => i.status === "PAID",
};

function Invoices() {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  useDocumentTitle(t("console.invoices.title"));
  const today = new Date();
  const [year, setYear] = useState(today.getFullYear());
  const [month, setMonth] = useState(today.getMonth() + 1);
  // The overview links here with ?filter=overdue.
  const asked = useSearchParams().get("filter") as Filter | null;
  const [filter, setFilter] = useState<Filter>(asked && FILTERS.includes(asked) ? asked : "all");
  const phone = useMediaQuery(NARROW);

  const query = useInfiniteQuery({
    queryKey: consoleKeys.invoiceMonth(scope, year, month),
    queryFn: ({pageParam}) => listConsoleInvoices(scope, year, month, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
  });

  const loaded = query.data?.pages.flatMap(page => page.data) ?? [];
  const invoices = loaded.filter(MATCH[filter]);
  const isCurrent = year === today.getFullYear() && month === today.getMonth() + 1;
  const label = new Intl.DateTimeFormat(currentLocale(), {month: "long", year: "numeric"})
    .format(new Date(year, month - 1, 1));

  function shift(by: number) {
    const d = new Date(year, month - 1 + by, 1);
    setYear(d.getFullYear());
    setMonth(d.getMonth() + 1);
  }

  return (
    <div className="space-y-6">
      <PageHeader title={t("console.invoices.title")}/>

      {/* The period and the status are the filters, so they sit above the
          table: they decide what the table *is*, not how it is sorted. */}
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-3">
        <div className="flex items-center gap-1">
          <Button variant="outline" size="sm" aria-label={t("console.invoices.prevMonth")} onClick={() => shift(-1)}>
            <ChevronLeft aria-hidden className="size-4"/>
          </Button>
          <span aria-live="polite" className="min-w-32 text-center text-sm text-foreground first-letter:uppercase">
            {label}
          </span>
          <Button variant="outline" size="sm" aria-label={t("console.invoices.nextMonth")} onClick={() => shift(1)}>
            <ChevronRight aria-hidden className="size-4"/>
          </Button>
          {!isCurrent && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setYear(today.getFullYear());
                setMonth(today.getMonth() + 1);
              }}
            >
              {t("console.invoices.thisMonth")}
            </Button>
          )}
        </div>
        <Segmented
          label={t("console.invoices.filter.label")}
          value={filter}
          onValueChange={setFilter}
          fill={phone}
          className="max-sm:w-full"
          options={FILTERS.map(f => ({value: f, label: t(`console.invoices.filter.${f}`)}))}
        />
      </div>

      {query.isPending && <TableSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && invoices.length === 0 && (
        <EmptyState
          icon={<FileText/>}
          title={t(loaded.length === 0 ? "console.invoices.empty" : "console.invoices.emptyFiltered")}
          action={loaded.length > 0
            ? <Button variant="outline" onClick={() => setFilter("all")}>{t("console.invoices.clearFilter")}</Button>
            : undefined}
        />
      )}

      {invoices.length > 0 && (
        <>
          <ul aria-label={t("console.invoices.title")} className="divide-y divide-border border-y border-border sm:hidden">
            {invoices.map(invoice => (
              <li key={invoice.id} className="space-y-1 py-3">
                <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-1">
                  <InvoiceLink invoice={invoice}/>
                  <InvoiceStatusBadge invoice={invoice}/>
                </div>
                <p className="truncate text-sm text-foreground">
                  {invoice.customer_name || <span className="text-muted-foreground">{invoice.customer_id}</span>}
                </p>
                <p className="flex justify-between gap-3 text-xs text-muted-foreground">
                  <span data-numeric>{t("console.invoices.dueOn", {date: compactDate(invoice.due_date)})}</span>
                  <span data-numeric className={invoice.amount_due > 0 ? "text-foreground" : undefined}>
                    {money(invoice.amount_due > 0 ? invoice.amount_due : invoice.total, invoice.currency)}
                  </span>
                </p>
              </li>
            ))}
          </ul>

          <div className="hidden overflow-x-auto sm:block">
            {/* The customer takes what is left and is cut there (whole on
                hover); dates are day and month, the month above names the year. */}
            <table className="w-full min-w-[40rem] table-fixed border-collapse text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  <th scope="col" className="w-28 py-2 pr-4 font-medium">{t("console.invoices.number")}</th>
                  <th scope="col" className="py-2 pr-4 font-medium">{t("console.invoices.customer")}</th>
                  <th scope="col" className="w-60 py-2 pr-4 font-medium">{t("console.invoices.status")}</th>
                  <th scope="col" className="w-32 py-2 pr-4 font-medium max-lg:hidden">{t("console.invoices.period")}</th>
                  <th scope="col" className="w-24 py-2 pr-4 font-medium">{t("console.invoices.due")}</th>
                  <th scope="col" className="w-28 py-2 pr-4 text-right font-medium">{t("console.invoices.total")}</th>
                  <th scope="col" className="w-28 py-2 text-right font-medium">{t("console.invoices.open")}</th>
                </tr>
              </thead>
              <tbody>
                {invoices.map(invoice => (
                  <tr key={invoice.id} className="border-b border-border last:border-0 hover:bg-surface">
                    <td className="py-2 pr-4">
                      {/* The number is the link. It is what an operator has in
                          front of them when somebody calls about a bill. */}
                      <InvoiceLink invoice={invoice}/>
                    </td>
                    <td className="truncate py-2 pr-4 text-foreground" title={invoice.customer_name || invoice.customer_id}>
                      {invoice.customer_name || (
                        <span className="text-muted-foreground">{invoice.customer_id}</span>
                      )}
                    </td>
                    <td className="py-2 pr-4">
                      <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <InvoiceStatusBadge invoice={invoice}/>
                        {/* "Já tentamos cobrar?" decides whether a bill is a
                            collection problem or an integration one; it is
                            news only from the second attempt on. */}
                        {invoice.attempt_count > 1 && (
                          <span className="text-xs text-muted-foreground">{t("console.invoices.attemptN", {count: invoice.attempt_count})}</span>
                        )}
                      </span>
                    </td>
                    <td data-numeric className="truncate py-2 pr-4 text-muted-foreground max-lg:hidden">
                      {compactPeriod(invoice.period.period_start, invoice.period.period_end)}
                    </td>
                    <td data-numeric className="py-2 pr-4 text-muted-foreground" title={shortDate(invoice.due_date)}>
                      {compactDate(invoice.due_date)}
                    </td>
                    <td data-numeric className="py-2 pr-4 text-right text-foreground">
                      {money(invoice.total, invoice.currency)}
                    </td>
                    <td
                      data-numeric
                      className={`py-2 text-right ${
                        invoice.amount_due > 0 ? "text-foreground" : "text-muted-foreground"
                      }`}
                    >
                      {money(invoice.amount_due, invoice.currency)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {query.hasNextPage && (
        <Button
          variant="outline"
          className="max-sm:w-full"
          disabled={query.isFetchingNextPage}
          onClick={() => void query.fetchNextPage()}
        >
          {t("console.invoices.more")}
        </Button>
      )}
    </div>
  );
}

function InvoiceLink({invoice}: { invoice: ConsoleInvoiceRow }) {
  const {t} = useTranslation();
  return (
    <Link
      href={`/console/invoice?id=${invoice.id}`}
      data-numeric
      className="font-medium text-brand-600 underline-offset-4 hover:underline"
    >
      {invoice.number ? t("console.invoices.numbered", {number: invoice.number}) : t("console.invoices.unnumbered")}
    </Link>
  );
}

function TableSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2, 3, 4].map(i => (
        <Skeleton key={i} className="h-8 w-full"/>
      ))}
    </div>
  );
}
