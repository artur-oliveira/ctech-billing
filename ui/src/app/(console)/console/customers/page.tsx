"use client"

import {EmptyState, Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import {Users} from "lucide-react"
import Link from "next/link"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {consoleKeys, listConsoleCustomers} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"

/**
 * C6 — the customers.
 *
 * The tax id is **masked here and everywhere in a listing**, and revealing it
 * is a separate, audited action on the detail (assessment § 15). A list that
 * printed every CPF would put the most sensitive column in the product on the
 * screen an operator leaves open all day.
 */
export default function ConsoleCustomersPage() {
  const mode = useMode()
  const query = useQuery({
    queryKey: consoleKeys.customers(mode),
    queryFn: () => listConsoleCustomers(mode),
  })

  const customers = query.data?.data ?? []

  return (
    <div className="space-y-6">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Clientes</h1>

      {query.isPending && <RowsSkeleton/>}
      {query.isError && <ErrorBlock error={query.error} onRetry={query.refetch}/>}

      {!query.isPending && !query.isError && customers.length === 0 && (
        <EmptyState
          icon={<Users/>}
          title="Nenhum cliente"
          description="Clientes criados por uma integração ou pelo console aparecem aqui."
        />
      )}

      {customers.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[40rem] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th scope="col" className="py-2 pr-4 font-medium">Nome</th>
                <th scope="col" className="py-2 pr-4 font-medium">E-mail</th>
                <th scope="col" className="py-2 pr-4 font-medium">CPF/CNPJ</th>
                <th scope="col" className="py-2 font-medium">Referência externa</th>
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
                      <span className="ml-2 text-xs text-muted-foreground">anonimizado</span>
                    )}
                  </td>
                  <td className="py-2 pr-4 text-muted-foreground">{customer.email || "—"}</td>
                  <td data-numeric className="py-2 pr-4 text-muted-foreground">
                    {customer.tax_id_masked || "—"}
                  </td>
                  <td className="py-2 text-muted-foreground">{customer.external_ref || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function RowsSkeleton() {
  return (
    <div className="space-y-2" aria-busy>
      {[0, 1, 2].map(i => <Skeleton key={i} className="h-8 w-full"/>)}
    </div>
  )
}
