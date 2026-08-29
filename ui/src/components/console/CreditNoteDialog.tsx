"use client"

import {Checkbox, Field, Input, Modal} from "@aoctech/ui"
import {useMutation} from "@tanstack/react-query"
import {useState} from "react"
import {toast} from "sonner"

import {messageFor} from "@/lib/api/client"
import {creditInvoice} from "@/lib/api/console"
import type {ConsoleInvoice} from "@/lib/api/consoleTypes"
import {useMode} from "@/lib/console/useMode"
import {money} from "@/lib/format"

/**
 * "Emitir nota de crédito", and the screen where immutability is taught rather
 * than hidden.
 *
 * There is no way to edit an issued invoice, here or anywhere: the correction
 * is a new document that references the old one. So the dialog states the
 * remaining credit as the ceiling, requires a reason — a credit nobody can
 * explain a year later is the one that matters most — and asks separately
 * whether money actually went back, because billing records refunds and never
 * performs them.
 */
export function CreditNoteDialog({
  open,
  invoice,
  credited,
  onClose,
  onIssued,
}: {
  open: boolean
  invoice: ConsoleInvoice
  credited: number
  onClose: () => void
  onIssued: () => void
}) {
  const mode = useMode()
  const remaining = Math.max(0, invoice.total - credited)
  const [amount, setAmount] = useState("")
  const [reason, setReason] = useState("")
  const [refunded, setRefunded] = useState(false)

  const cents = Math.round(Number(amount.replace(",", ".")) * 100)
  const valid = Number.isFinite(cents) && cents > 0 && cents <= remaining && reason.trim() !== ""

  const issue = useMutation({
    mutationFn: () =>
      creditInvoice(
        invoice.id,
        {amount: cents, reason: reason.trim(), refunded_externally: refunded},
        mode,
      ),
    onSuccess: () => {
      toast.success("Nota de crédito emitida.")
      setAmount("")
      setReason("")
      setRefunded(false)
      onIssued()
    },
    // The server re-checks the ceiling against freshly read totals, so this is
    // the message that arrives when another operator credited the same invoice
    // between this dialog opening and being submitted.
    onError: error => toast.error(messageFor(error)),
  })

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Emitir nota de crédito"
      description={`A fatura não é alterada: o crédito é um documento novo que aponta para ela. Ainda pode ser creditado ${money(remaining, invoice.currency)}.`}
      cancelLabel="Cancelar"
      submitLabel="Emitir crédito"
      submitDisabled={!valid}
      loading={issue.isPending}
      onSubmit={() => issue.mutate()}
    >
      <div className="space-y-4">
        <Field label="Valor" htmlFor="credit-amount" hint={`Máximo ${money(remaining, invoice.currency)}`}>
          <Input
            id="credit-amount"
            inputMode="decimal"
            placeholder="0,00"
            value={amount}
            onChange={event => setAmount(event.target.value)}
          />
        </Field>

        <Field
          label="Motivo"
          htmlFor="credit-reason"
          hint="Fica no histórico e é o que explica este crédito depois."
        >
          <Input
            id="credit-reason"
            value={reason}
            onChange={event => setReason(event.target.value)}
            placeholder="Cobrança em duplicidade"
          />
        </Field>

        {/* The label is written here rather than passed as a prop: the
            primitive is Base UI's bare Root, and the sentence under it is the
            part that matters — a checkbox that quietly records a refund nobody
            made is a history that lies. */}
        <label className="flex items-start gap-2.5 text-sm text-foreground">
          <Checkbox
            checked={refunded}
            onCheckedChange={setRefunded}
            className="mt-0.5 shrink-0"
          />
          <span>
            O dinheiro já foi devolvido pelo wallet
            <span className="mt-0.5 block text-xs text-muted-foreground">
              O billing registra a devolução; ele não devolve. Marcar aqui sem devolver de fato
              deixa o histórico dizendo algo que não aconteceu.
            </span>
          </span>
        </label>
      </div>
    </Modal>
  )
}
