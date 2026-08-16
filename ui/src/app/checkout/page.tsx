"use client"

import {Button, Separator, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery} from "@tanstack/react-query"
import Image from "next/image"
import {useSearchParams} from "next/navigation"
import {Suspense, useState} from "react"
import {toast} from "sonner"

import {StatusScreen} from "@/components/StatusScreen"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {Money} from "@/components/portal/Money"
import {PixPanel} from "@/components/portal/PixPanel"
import {StatusBadge} from "@/components/portal/StatusBadge"
import {checkoutKeys, getCheckout, payCheckout} from "@/lib/api/checkout"
import {messageFor, statusOf} from "@/lib/api/client"
import type {PixPayment} from "@/lib/api/types"
import {longDate, money} from "@/lib/format"

/**
 * X1 — the payment link. The only screen here a stranger can pay from.
 *
 * It is deliberately outside the portal shell: there is no nav, because there
 * is nowhere else this reader can go, and a header offering "Faturas" and
 * "Assinaturas" to somebody with no session is three links to a login wall.
 *
 * The merchant's name is the first thing on the page. A payment screen that
 * does not say who is being paid is indistinguishable from a phishing page, and
 * this one arrives by e-mail — the exact channel phishing arrives by.
 *
 * Nothing here identifies the payer. The server's payload carries no name, no
 * e-mail and no tax id (ADR 0009 § minimization), so a link forwarded to the
 * wrong person discloses an amount and a plan name, and that is all.
 */
export default function CheckoutPage() {
  return (
    <Suspense fallback={<CheckoutSkeleton />}>
      <CheckoutScreen />
    </Suspense>
  )
}

/** How often the page asks whether the charge landed, while one is open.
 *  There is no stream on the public routes — see PixPanel. Four seconds is
 *  fast enough that the confirmation feels immediate and slow enough that a
 *  thirty-minute window is 450 requests rather than 1800. */
const POLL_MS = 4_000

function CheckoutScreen() {
  const token = useSearchParams().get("token") ?? ""

  // The open charge is component state, not query data, and that distinction is
  // load-bearing: `GET /checkout/:token` answers without a `payment`, because
  // opening a link must never open a charge. Caching the pay response under the
  // view's key would therefore have the first poll — four seconds later — wipe
  // the PIX code off the screen of somebody in the middle of scanning it.
  const [payment, setPayment] = useState<PixPayment | null>(null)

  const query = useQuery({
    queryKey: checkoutKeys.view(token),
    queryFn: () => getCheckout(token),
    enabled: token !== "",
    // Polling starts only once a charge is open, because that is the only
    // window where the answer can change. Before it, re-reading every four
    // seconds would be a request per reader per four seconds for as long as
    // somebody stares at the page deciding whether to pay.
    refetchInterval: q =>
      payment && (q.state.data?.invoice.amount_due ?? 0) > 0 ? POLL_MS : false,
  })

  const pay = useMutation({
    mutationFn: () => payCheckout(token),
    onSuccess: result => setPayment(result.payment ?? null),
    onError: error => toast.error(messageFor(error)),
  })

  if (token === "" || (query.isError && statusOf(query.error) === 404)) return <Invalid />
  if (query.isPending) return <CheckoutSkeleton />
  if (query.isError) {
    return (
      <Shell merchant="">
        <ErrorBlock error={query.error} onRetry={query.refetch} />
      </Shell>
    )
  }

  const {merchant, invoice} = query.data
  const settled = invoice.amount_due === 0

  return (
    <Shell merchant={merchant}>
      <header className="space-y-3">
        <p className="text-sm text-muted-foreground">
          {invoice.number ? `Fatura nº ${invoice.number}` : "Fatura"}
        </p>
        <h1>
          <Money cents={invoice.amount_due} currency={invoice.currency} size="hero" />
        </h1>
        <StatusBadge state={invoice.state} tone={invoice.tone} />
        <p className="text-pretty text-sm text-muted-foreground">
          {invoice.description} · vence em {longDate(invoice.due_date)}
        </p>
      </header>

      {settled ? (
        <div className="flex items-start gap-3 rounded-xl bg-success px-5 py-4 text-background">
          <div className="space-y-1">
            <p className="font-medium">Pagamento recebido</p>
            <p className="text-sm opacity-90">
              Esta fatura está quitada. Você pode fechar esta página.
            </p>
          </div>
        </div>
      ) : payment ? (
        <PixPanel
          payment={payment}
          settled={settled}
          amount={invoice.amount_due}
          currency={invoice.currency}
          onPaid={() => void query.refetch()}
          onRegenerate={() => pay.mutate()}
          regenerating={pay.isPending}
        />
      ) : invoice.payable ? (
        <Button block onClick={() => pay.mutate()} disabled={pay.isPending}>
          {pay.isPending
            ? "Gerando o código…"
            : `Pagar ${money(invoice.amount_due, invoice.currency)} com PIX`}
        </Button>
      ) : (
        <div className="rounded-xl border border-border bg-surface px-5 py-4">
          <p className="text-pretty text-sm text-foreground">
            Esta fatura não está aberta para pagamento. Nada foi cobrado — fale com {merchant} se
            você acha que isso está errado.
          </p>
        </div>
      )}

      {invoice.lines && invoice.lines.length > 0 && (
        <section aria-labelledby="linhas" className="space-y-4">
          <h2 id="linhas" className="text-sm font-medium text-muted-foreground">
            O que está sendo cobrado
          </h2>
          <ul className="space-y-3">
            {invoice.lines.map((line, i) => (
              <li key={i} className="flex items-baseline justify-between gap-4">
                <div className="min-w-0">
                  <p className="text-sm text-foreground">{line.description}</p>
                  {line.proration && (
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      Proporcional aos dias usados neste período
                    </p>
                  )}
                </div>
                <Money cents={line.amount} currency={invoice.currency} className="text-sm" />
              </li>
            ))}
          </ul>
          <Separator />
          <div className="flex items-baseline justify-between gap-4">
            <span className="text-sm font-medium text-foreground">Total</span>
            <Money cents={invoice.amount_due} currency={invoice.currency} />
          </div>
        </section>
      )}
    </Shell>
  )
}

/**
 * The page's own chrome. It carries the CTech mark because the reader arrived
 * from an e-mail and needs to know whose page this is, and the merchant's name
 * because they need to know who the money goes to. Those are two different
 * facts and the page says both.
 */
function Shell({merchant, children}: {merchant: string; children: React.ReactNode}) {
  return (
    <div data-density="comfortable" className="min-h-dvh">
      <header className="border-b border-border">
        <div className="mx-auto flex h-16 max-w-md items-center gap-2.5 px-4">
          <Image
            src="/android-chrome-192x192.png"
            alt=""
            width={28}
            height={28}
            priority
            className="size-7 rounded-lg"
          />
          <span className="text-base font-semibold tracking-[-0.02em] text-brand-600">
            CTech
            <span className="ml-1.5 font-normal text-muted-foreground">Billing</span>
          </span>
        </div>
      </header>
      <main className="mx-auto max-w-md space-y-8 px-4 py-8 pb-20">
        {merchant && (
          <p className="text-sm text-muted-foreground">
            Cobrança de <span className="font-medium text-foreground">{merchant}</span>
          </p>
        )}
        {children}
      </main>
    </div>
  )
}

/**
 * An expired or forged token. Both say the same thing, and on purpose: a page
 * that distinguished "this link expired" from "this link never existed" would
 * be an oracle telling somebody guessing tokens when they had guessed right.
 */
function Invalid() {
  return (
    <StatusScreen
      title="Este link não é mais válido"
      description="Links de pagamento expiram por segurança. Nada foi cobrado. Peça um novo link a quem enviou este, ou entre na sua conta CTech para ver a fatura."
    />
  )
}

function CheckoutSkeleton() {
  return (
    <Shell merchant="">
      <div className="space-y-8" aria-busy>
        <div className="space-y-3">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-9 w-52" />
          <Skeleton className="h-5 w-32 rounded-full" />
          <Skeleton className="h-4 w-64" />
        </div>
        <Skeleton className="h-11 w-full rounded-lg" />
      </div>
    </Shell>
  )
}
