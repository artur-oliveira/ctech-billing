"use client"

import limits from "@/lib/limits.json"
import {Checkbox, Field, Input} from "@aoctech/ui"
import {useMutation} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {Modal} from "@/components/ui/ConsoleOverlay"
import {messageFor} from "@/lib/api/client"
import {useFieldErrors} from "@/lib/useFieldErrors"
import {creditInvoice} from "@/lib/api/console"
import type {ConsoleInvoice} from "@/lib/api/consoleTypes"
import {useMode} from "@/lib/console/useMode"
import {money} from "@/lib/format"
import {maskMoney, moneyPlaceholder, parseMoney} from "@/lib/money"

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
  const {t} = useTranslation()
  const mode = useMode()
  const remaining = Math.max(0, invoice.total - credited)
  const [amount, setAmount] = useState("")
  const [reason, setReason] = useState("")
  const [refunded, setRefunded] = useState(false)

  const cents = parseMoney(amount) ?? 0
  const valid = cents > 0 && cents <= remaining && reason.trim() !== ""

  const fe = useFieldErrors(["amount", "reason"], {keepOthers: false})
  const issue = useMutation({
    mutationFn: () =>
      creditInvoice(
        invoice.id,
        {amount: cents, reason: reason.trim(), refunded_externally: refunded},
        mode,
      ),
    onSuccess: () => {
      toast.success(t("console.credit.done"))
      setAmount("")
      setReason("")
      setRefunded(false)
      onIssued()
    },
    // The server re-checks the ceiling against freshly read totals, so this is
    // the message that arrives when another operator credited the same invoice
    // between this dialog opening and being submitted.
    onError: error => { if (!fe.set(error)) toast.error(messageFor(error)) },
  })

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("console.credit.title")}
      description={t("console.credit.description", {amount: money(remaining, invoice.currency)})}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.credit.submit")}
      submitDisabled={!valid}
      loading={issue.isPending}
      onSubmit={() => { fe.reset(); issue.mutate() }}
    >
      <div className="space-y-4">
        <Field label={t("console.credit.amount")} htmlFor="credit-amount" error={fe.of("amount")} hint={t("console.credit.max", {amount: money(remaining, invoice.currency)})}>
          <Input
            id="credit-amount"
            inputMode="decimal"
            placeholder={moneyPlaceholder()}
            value={amount}
            {...fe.props("amount", "credit-amount")}
            onChange={event => { setAmount(maskMoney(event.target.value)); fe.clear("amount") }}
          />
        </Field>

        <Field
          label={t("console.credit.reason")}
          htmlFor="credit-reason"
          error={fe.of("reason")}
          hint={t("console.credit.reasonHint")}
        >
          <Input
            id="credit-reason"
            maxLength={limits.text.reason}
            value={reason}
            {...fe.props("reason", "credit-reason")}
            onChange={event => { setReason(event.target.value); fe.clear("reason") }}
            placeholder={t("console.credit.reasonPlaceholder")}
          />
        </Field>

        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}

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
            {t("console.credit.refunded")}
            <span className="mt-0.5 block text-xs text-muted-foreground">
              {t("console.credit.refundedNote")}
            </span>
          </span>
        </label>
      </div>
    </Modal>
  )
}
