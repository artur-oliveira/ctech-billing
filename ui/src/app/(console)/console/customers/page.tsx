"use client";

import {Button, EmptyState, PageHeader, Skeleton} from "@aoctech/ui";
import {useInfiniteQuery} from "@tanstack/react-query";
import {Users} from "lucide-react";
import Link from "next/link";
import {useTranslation} from "react-i18next";

import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {consoleKeys, listConsoleCustomers} from "@/lib/api/console";
import {useConsoleScope} from "@/lib/console/scope";
import {formatTaxID} from "@/lib/format";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";

/**
 * C6 — the customers.
 *
 * The tax id is **masked here and everywhere in a listing**, and revealing it
 * is a separate, audited action on the detail (assessment § 15). A list that
 * printed every CPF would put the most sensitive column in the product on the
 * screen an operator leaves open all day.
 */
export default function ConsoleCustomersPage() {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  useDocumentTitle(t("console.customers.title"));
  const query = useInfiniteQuery({
    queryKey: consoleKeys.customers(scope),
    queryFn: ({pageParam}) => listConsoleCustomers(scope, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => (last.has_more ? last.cursor : undefined),
  });

  const customers = query.data?.pages.flatMap(page => page.data) ?? [];

  return (
    <div className="space-y-6">
      <PageHeader title={t("console.customers.title")}/>

      {query.isPending && <RowsSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && customers.length === 0 && (
        <EmptyState
          icon={<Users/>}
          title={t("console.customers.empty")}
        />
      )}

      {customers.length > 0 && (
        <ul aria-label={t("console.customers.title")} className="divide-y divide-border border-y border-border sm:hidden">
          {customers.map(customer => (
            <li key={customer.id} className="space-y-0.5 py-3">
              <Link href={`/console/customer?id=${customer.id}`} className="font-medium text-brand-600 underline-offset-4 hover:underline">
                {customer.name}
              </Link>
              {customer.anonymized && (
                <span className="ml-2 text-xs text-muted-foreground">{t("console.customers.anonymized")}</span>
              )}
              <p className="truncate text-sm text-muted-foreground">{customer.email || "—"}</p>
              {customer.tax_id_masked && (
                <p data-numeric className="text-xs text-muted-foreground">{formatTaxID(customer.tax_id_masked)}</p>
              )}
            </li>
          ))}
        </ul>
      )}

      {customers.length > 0 && (
        <div className="hidden overflow-x-auto sm:block">
          <table className="w-full min-w-[40rem] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.customers.name")}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.customers.email")}</th>
                <th scope="col" className="py-2 pr-4 font-medium">{t("console.customers.taxId")}</th>
                <th scope="col" className="py-2 font-medium">{t("console.customers.externalRef")}</th>
              </tr>
            </thead>
            <tbody>
              {customers.map(customer => (
                <tr
                  key={customer.id}
                  className="border-b border-border last:border-0 hover:bg-surface"
                >
                  <td className="py-2 pr-4">
                    <Link
                      href={`/console/customer?id=${customer.id}`}
                      className="text-brand-600 underline-offset-4 hover:underline"
                    >
                      {customer.name}
                    </Link>
                    {customer.anonymized && (
                      <span className="ml-2 text-xs text-muted-foreground">{t("console.customers.anonymized")}</span>
                    )}
                  </td>
                  <td className="max-w-64 truncate py-2 pr-4 text-muted-foreground" title={customer.email}>{customer.email || "—"}</td>
                  <td data-numeric className="py-2 pr-4 text-muted-foreground">
                    {customer.tax_id_masked ? formatTaxID(customer.tax_id_masked) : "—"}
                  </td>
                  <td className="truncate py-2 font-mono text-xs text-muted-foreground" title={customer.external_ref}>{customer.external_ref || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {query.hasNextPage && (
        <Button
          variant="outline"
          className="max-sm:w-full"
          disabled={query.isFetchingNextPage}
          onClick={() => void query.fetchNextPage()}
        >
          {t("console.customers.more")}
        </Button>
      )}
    </div>
  );
}

function RowsSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2].map(i => <Skeleton key={i} className="h-8 w-full"/>)}
    </div>
  );
}
