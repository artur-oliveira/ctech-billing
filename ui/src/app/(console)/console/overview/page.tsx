"use client"

import {Skeleton} from "@aoctech/ui"
import {useQuery} from "@tanstack/react-query"
import Link from "next/link"

import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {consoleKeys, getConsoleOverview} from "@/lib/api/console"
import {useMode} from "@/lib/console/useMode"
import {money} from "@/lib/format"

/**
 * C1 — "algo precisa de mim hoje?"
 *
 * Not a grid of decorative metric cards. Three amounts on one rule, and below
 * them only the counts that are actually a call to action — a draft nothing
 * will ever pick up, and how many bills make up the overdue total. One large
 * overdue invoice and twenty small ones are the same amount and completely
 * different problems.
 *
 * Every figure is *this month*, said out loud, and the screen states when it
 * could not count the whole month rather than presenting a partial sum as a
 * total. A merchant who plans around a wrong "recebido" is worse off than one
 * who opens the list.
 */
export default function ConsoleOverviewPage() {
  const mode = useMode()
  const today = new Date()
  const year = today.getFullYear()
  const month = today.getMonth() + 1

  const query = useQuery({
    queryKey: consoleKeys.overview(mode, year, month),
    queryFn: () => getConsoleOverview(year, month, mode),
  })

  const label = new Intl.DateTimeFormat("pt-BR", {month: "long", year: "numeric"})
    .format(new Date(year, month - 1, 1))

  if (query.isPending) return <OverviewSkeleton/>
  if (query.isError) return <ErrorBlock error={query.error} onRetry={query.refetch}/>

  const data = query.data
  const quiet =
    data.overdue === 0 && data.drafts === 0 && data.open === 0

  return (
    <div className="space-y-8">
      <div className="space-y-1">
        <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Visão geral</h1>
        <p className="text-sm text-muted-foreground first-letter:uppercase">{label}</p>
      </div>

      <dl className="grid gap-x-6 gap-y-6 border-y border-border py-5 sm:grid-cols-3">
        <Amount label="Recebido no mês" cents={data.received}/>
        <Amount label="Em aberto, no prazo" cents={data.open}/>
        <Amount label="Vencido" cents={data.overdue} urgent={data.overdue > 0}/>
      </dl>

      <section aria-labelledby="acoes" className="space-y-3">
        <h2 id="acoes" className="text-sm font-medium text-muted-foreground">
          {quiet ? "Nada pedindo atenção" : "Precisa de você"}
        </h2>

        {quiet ? (
          <p className="text-sm text-muted-foreground">
            Nenhuma fatura vencida, nenhum rascunho parado e nada em aberto neste mês.
          </p>
        ) : (
          <ul className="divide-y divide-border border-y border-border">
            {data.overdue_count > 0 && (
              <Item
                href="/console/invoices"
                title={`${data.overdue_count} fatura${data.overdue_count === 1 ? "" : "s"} vencida${data.overdue_count === 1 ? "" : "s"}`}
                detail={`${money(data.overdue)} em atraso. A cobrança automática continua, mas quem paga é o cliente.`}
              />
            )}
            {data.drafts > 0 && (
              <Item
                href="/console/invoices"
                title={`${data.drafts} rascunho${data.drafts === 1 ? "" : "s"} sem emitir`}
                detail="Uma fatura em rascunho não é cobrada de ninguém e nada vai emiti-la sozinho — abra e emita."
              />
            )}
            {data.uncollectible > 0 && (
              <Item
                href="/console/invoices"
                title={`${data.uncollectible} dada${data.uncollectible === 1 ? "" : "s"} por perdida${data.uncollectible === 1 ? "" : "s"}`}
                detail="Fim da política de cobrança. Continua devida — o que mudou é que o billing parou de esperar."
              />
            )}
          </ul>
        )}
      </section>

      {!data.complete && (
        <p className="text-xs text-muted-foreground">
          Este mês tem mais faturas do que cabem em uma leitura, então os valores acima somam as{" "}
          {data.counted} primeiras. A lista de faturas mostra todas.
        </p>
      )}
    </div>
  )
}

function Amount({label, cents, urgent}: { label: string; cents: number; urgent?: boolean }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd
        data-numeric
        className={`text-2xl font-semibold tracking-[-0.015em] ${
          urgent ? "text-danger" : "text-foreground"
        }`}
      >
        {money(cents)}
      </dd>
    </div>
  )
}

function Item({href, title, detail}: { href: string; title: string; detail: string }) {
  return (
    <li>
      <Link href={href} className="-mx-3 block rounded-lg px-3 py-3 hover:bg-surface">
        <p className="text-sm font-medium text-foreground">{title}</p>
        <p className="text-sm text-muted-foreground">{detail}</p>
      </Link>
    </li>
  )
}

function OverviewSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-6 w-40"/>
      <Skeleton className="h-24 w-full"/>
      <Skeleton className="h-20 w-full"/>
    </div>
  )
}
