"use client"

import {Button, Input} from "@aoctech/ui"
import {Modal} from "@/components/ui/ConsoleOverlay"
import {useMutation} from "@tanstack/react-query"
import {Plus, X} from "lucide-react"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import type {TFunction} from "i18next"
import {toast} from "sonner"

import {messageFor} from "@/lib/api/client"
import {useFieldErrors} from "@/lib/useFieldErrors"
import type {DunningAction, DunningPolicy, DunningStep} from "@/lib/api/consoleTypes"

/**
 * The dunning schedule, read and edited.
 *
 * It is a **list of days**, not a form of named fields, because that is what
 * the policy is: an ordered sequence of things that happen to an unpaid
 * invoice. A form with "primeiro lembrete", "segundo lembrete" would fix the
 * shape at whatever the default happens to be today.
 *
 * The sentence under the editor is the one thing an operator must understand
 * before saving: a policy change does not touch invoices already being chased.
 * The schedule is copied onto an invoice when it is issued, so shortening the
 * policy has not just moved everybody's write-off date three weeks forward.
 */
const ACTIONS: DunningAction[] = ["remind", "escalate", "abandon"]

export function DunningPolicyCard({
  title,
  description,
  policy,
  inheritLabel,
  onSave,
}: {
  title: string
  description: string
  policy: DunningPolicy
  /** What clearing the policy falls back to, named so "limpar" is not a
   *  guess: at the organization it is the built-in default, at a product it is
   *  the organization's. */
  inheritLabel: string
  onSave: (steps: DunningStep[]) => Promise<unknown>
}) {
  const {t} = useTranslation()
  const [editing, setEditing] = useState(false)

  return (
    <section aria-labelledby="dunning" className="space-y-3">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 id="dunning" className="text-sm font-medium text-foreground">{title}</h2>
          <p className="max-w-prose text-sm text-muted-foreground">{description}</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
          {t("console.dunning.edit")}
        </Button>
      </div>

      {!policy.custom && (
        <p className="text-xs text-muted-foreground">
          {t("console.dunning.inherited", {label: inheritLabel})}
        </p>
      )}

      <ol className="divide-y divide-border border-y border-border">
        {policy.steps.map((step, i) => (
          <li key={i} className="flex items-baseline justify-between gap-4 py-2 text-sm">
            <span className="text-foreground">{t(`console.dunning.${step.action}`, {defaultValue: step.action})}</span>
            <span data-numeric className="text-muted-foreground">{dayLabel(t, step.offset)}</span>
          </li>
        ))}
      </ol>

      {/* Keyed on the stored policy and mounted only while open, which is how
          cancelling and re-opening starts from what is saved rather than from
          the abandoned edit — the draft dies with the component instead of
          being reset by an effect that fights its own state. */}
      {editing && (
        <PolicyEditor
          key={JSON.stringify(policy.steps)}
          policy={policy}
          inheritLabel={inheritLabel}
          onClose={() => setEditing(false)}
          onSave={onSave}
        />
      )}
    </section>
  )
}

function dayLabel(t: TFunction, offset: number): string {
  if (offset === 0) return t("console.dunning.onDue")
  if (offset < 0) return t("console.dunning.before", {count: -offset})
  return t("console.dunning.after", {count: offset})
}

function PolicyEditor({
  policy,
  inheritLabel,
  onClose,
  onSave,
}: {
  policy: DunningPolicy
  inheritLabel: string
  onClose: () => void
  onSave: (steps: DunningStep[]) => Promise<unknown>
}) {
  const {t} = useTranslation()
  const [steps, setSteps] = useState<DunningStep[]>(policy.steps)

  // An error on "steps[2].offset" lands under the third row; one on "steps" as a whole is general.
  const fe = useFieldErrors([...steps.map((_, i) => `steps[${i}]`), "steps"], {keepOthers: false})
  const save = useMutation({
    mutationFn: (next: DunningStep[]) => onSave(next),
    onSuccess: () => {
      toast.success(t("console.dunning.saved"))
      onClose()
    },
    // The server re-validates: ordered days, at most one "dar por perdida" and
    // por último, nada de restringir acesso antes do vencimento.
    onError: error => { if (!fe.set(error)) toast.error(messageFor(error)) },
  })

  return (
    <Modal
      open
      onClose={onClose}
      title={t("console.dunning.title")}
      description={t("console.dunning.body")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.dunning.save")}
      loading={save.isPending}
      onSubmit={() => { fe.reset(); save.mutate(steps) }}
      size="lg"
    >
      <div className="space-y-4">
        <ul className="space-y-2">
          {steps.map((step, i) => (
            <li key={i} className="flex flex-wrap items-center gap-2">
              <Input
                aria-label={t("console.dunning.stepDay", {n: i + 1})}
                maxLength={3}
                inputMode="numeric"
                value={String(step.offset)}
                {...fe.props(`steps[${i}]`, `dunning-step-${i}`)}
                id={`dunning-step-${i}`}
                onChange={event => {
                  setSteps(replace(steps, i, {...step, offset: Number(event.target.value) || 0}))
                  fe.clear(`steps[${i}]`)
                }}
                className="w-24"
              />
              <select
                aria-label={t("console.dunning.stepAction", {n: i + 1})}
                value={step.action}
                onChange={event =>
                  setSteps(replace(steps, i, {...step, action: event.target.value as DunningAction}))
                }
                className="h-9 min-w-40 max-w-full rounded-lg border border-border bg-background px-2 text-sm text-foreground"
              >
                {ACTIONS.map(action => (
                  <option key={action} value={action}>{t(`console.dunning.${action}`)}</option>
                ))}
              </select>
              <span className="text-xs text-muted-foreground">{dayLabel(t, step.offset)}</span>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t("console.dunning.stepRemove", {n: i + 1})}
                onClick={() => { fe.reset(); setSteps(steps.filter((_, j) => j !== i)) }}
              >
                <X aria-hidden className="size-4"/>
              </Button>
              {fe.of(`steps[${i}]`) && (
                <p id={`dunning-step-${i}-error`} role="alert" className="w-full text-sm text-danger">{fe.of(`steps[${i}]`)}</p>
              )}
            </li>
          ))}
        </ul>

        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              setSteps([...steps, {offset: (steps.at(-1)?.offset ?? 0) + 1, action: "remind"}])
            }
          >
            <Plus aria-hidden className="size-3.5"/>
            {t("console.dunning.add")}
          </Button>
          {/* Clearing is not "disable dunning" and must not read as it: an
              invoice that is never chased and never written off sits em aberto
              para sempre parecendo receita. */}
          <Button variant="ghost" size="sm" onClick={() => { fe.reset(); setSteps([]) }}>
            {t("console.dunning.reset", {label: inheritLabel})}
          </Button>
        </div>

        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}

        {steps.length === 0 && (
          <p className="text-sm text-muted-foreground">
            {t("console.dunning.none", {label: inheritLabel})}
          </p>
        )}
      </div>
    </Modal>
  )
}

function replace(steps: DunningStep[], index: number, step: DunningStep): DunningStep[] {
  return steps.map((current, i) => (i === index ? step : current))
}
