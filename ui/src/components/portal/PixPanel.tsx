"use client"

import {Button, Separator} from "@aoctech/ui"
import {QRCodeSVG} from "qrcode.react"
import {Check, Copy} from "lucide-react"
import {useCallback, useEffect, useState} from "react"
import {toast} from "sonner"

import {Money} from "@/components/portal/Money"
import type {Cents, PixPayment} from "@/lib/api/types"
import {countdown} from "@/lib/format"
import {usePaymentStream} from "@/lib/hooks/usePaymentStream"

/**
 * The PIX charge, once it is open.
 *
 * This is the one surface in the whole product optimised for finishing a task
 * rather than for density: one code, one countdown, one confirmation. Anything
 * else on it is something to read instead of paying.
 *
 * Two callers watch for settlement two different ways, and the panel takes
 * either. The portal has an invoice id and a session, so it streams
 * (`/v1/portal/invoices/:id/events`). The checkout link has neither — the
 * public routes are the token and nothing else — so it polls its own endpoint
 * and passes the answer down as `settled`. The hook is still called
 * unconditionally, with `enabled` false, because a hook behind an `if` is a
 * crash the first time a caller switches.
 */
export function PixPanel({
                           invoiceId,
                           settled = false,
                           payment,
                           amount,
                           currency,
                           onPaid,
                           onRegenerate,
                           regenerating,
                         }: {
  /** Portal only. Absent on the checkout link, which has no invoice id to
   *  stream and no session to stream it with. */
  invoiceId?: string
  /** Checkout only: the parent's poll says the charge landed. */
  settled?: boolean
  payment: PixPayment
  amount: Cents
  currency: string
  onPaid: () => void
  onRegenerate: () => void
  regenerating: boolean
}) {
  const secondsLeft = useCountdown(payment.expires_at)
  const expired = secondsLeft <= 0
  const streamed = usePaymentStream(invoiceId ?? "", Boolean(invoiceId) && !expired)
  const paid = settled || streamed === "paid"
  const status = paid ? "paid" : streamed

  useEffect(() => {
    if (paid) onPaid()
  }, [paid, onPaid])

  if (paid) {
    return (
      <div className="rounded-xl border border-success/20 bg-success/5 px-5 py-6 text-center">
        <Check aria-hidden className="mx-auto size-6 text-success"/>
        <p className="mt-3 font-medium text-foreground">Pagamento confirmado</p>
        <p className="mt-1 text-sm text-muted-foreground">
          Recebemos <Money cents={amount} currency={currency} className="text-sm"/>. Nada mais a
          fazer.
        </p>
      </div>
    )
  }

  if (expired) {
    return (
      // The way out is on the screen. An expiry notice that tells somebody to
      // generate a new code and gives them nothing to press is a dead end with
      // instructions written on it.
      <div className="rounded-xl border border-border bg-surface px-5 py-6 text-center">
        <p className="font-medium text-foreground">Este código PIX expirou</p>
        <p className="mx-auto mt-1 max-w-[46ch] text-pretty text-sm text-muted-foreground">
          Nada foi cobrado. Gere um novo código para pagar — o valor e a fatura são os mesmos.
        </p>
        <Button className="mt-4" onClick={onRegenerate} disabled={regenerating}>
          {regenerating ? "Gerando…" : "Gerar novo código"}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-5 rounded-xl border border-border p-5 shadow-card">
      <div className="flex items-baseline justify-between gap-4">
        <p className="text-sm font-medium text-foreground">Pague com PIX</p>
        <p data-numeric className="text-sm text-muted-foreground">
          expira em {countdown(secondsLeft)}
        </p>
      </div>

      <div className="flex justify-center">
        {/* Level M keeps the code scannable with a phone camera at an angle,
            which is how it is actually used. */}
        <QRCodeSVG
          value={payment.pix_code}
          size={188}
          level="M"
          marginSize={2}
          bgColor="#ffffff"
          fgColor="#211816"
          title={`Código PIX da fatura, ${payment.pix_code.length} caracteres`}
        />
      </div>

      <Separator/>

      <div className="space-y-2">
        <p className="text-sm text-muted-foreground">
          Ou copie o código e cole no app do seu banco:
        </p>
        <CopyCode code={payment.pix_code}/>
      </div>

      <p
        className="text-center text-sm text-muted-foreground"
        role="status"
        aria-live="polite"
      >
        {status === "lost" && invoiceId
          ? "Não estamos conseguindo acompanhar em tempo real. Pode pagar assim mesmo — atualize a página depois."
          : "Assim que o pagamento cair, esta tela avisa sozinha."}
      </p>
    </div>
  )
}

function CopyCode({code}: { code: string }) {
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      setTimeout(() => setCopied(false), 2500)
    } catch {
      // Clipboard access can be denied outright, and telling somebody "copiado"
      // when nothing was copied is worse than telling them to select it.
      toast.error("Não foi possível copiar. Selecione o código e copie manualmente.")
    }
  }

  return (
    <div className="flex items-stretch gap-2">
      <code
        className="min-w-0 flex-1 truncate rounded-lg bg-surface px-3 py-2.5 font-mono text-xs text-muted-foreground">
        {code}
      </code>
      <Button variant="outline" onClick={copy} className="shrink-0">
        {copied ? <Check aria-hidden/> : <Copy aria-hidden/>}
        {copied ? "Copiado" : "Copiar"}
      </Button>
    </div>
  )
}

/** Seconds until `iso`, ticking once a second and never below zero. */
function useCountdown(iso: string): number {
  const remaining = useCallback(
    () => Math.max(0, Math.floor((new Date(iso).getTime() - Date.now()) / 1000)),
    [iso]
  )
  const [seconds, setSeconds] = useState(remaining)

  // A new charge resets the clock during render, not inside the effect: setting
  // it there paints one frame of the old code's remaining time first.
  const [prevIso, setPrevIso] = useState(iso)
  if (prevIso !== iso) {
    setPrevIso(iso)
    setSeconds(remaining())
  }

  useEffect(() => {
    const timer = setInterval(() => {
      const left = remaining()
      setSeconds(left)
      if (left <= 0) clearInterval(timer)
    }, 1000)
    return () => clearInterval(timer)
  }, [remaining])

  return seconds
}
