"use client"

import {Button, Input, Modal} from "@aoctech/ui"
import {useMutation} from "@tanstack/react-query"
import {Plus, X} from "lucide-react"
import {useState} from "react"
import {toast} from "sonner"

import {messageFor} from "@/lib/api/client"
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
const ACTION_LABEL: Record<DunningAction, string> = {
  remind: "Enviar lembrete",
  escalate: "Restringir o acesso",
  abandon: "Dar a dívida por perdida",
}

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
  const [editing, setEditing] = useState(false)

  return (
    <section aria-labelledby="dunning" className="space-y-3">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 id="dunning" className="text-sm font-medium text-foreground">{title}</h2>
          <p className="max-w-prose text-sm text-muted-foreground">{description}</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
          Editar política
        </Button>
      </div>

      {!policy.custom && (
        <p className="text-xs text-muted-foreground">
          Herdada — {inheritLabel}. Está em vigor do mesmo jeito.
        </p>
      )}

      <ol className="divide-y divide-border border-y border-border">
        {policy.steps.map((step, i) => (
          <li key={i} className="flex items-baseline justify-between gap-4 py-2 text-sm">
            <span className="text-foreground">{ACTION_LABEL[step.action] ?? step.action}</span>
            <span data-numeric className="text-muted-foreground">{dayLabel(step.offset)}</span>
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

function dayLabel(offset: number): string {
  if (offset === 0) return "no vencimento"
  if (offset < 0) return `${-offset} dia${offset === -1 ? "" : "s"} antes do vencimento`
  return `${offset} dia${offset === 1 ? "" : "s"} depois do vencimento`
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
  const [steps, setSteps] = useState<DunningStep[]>(policy.steps)

  const save = useMutation({
    mutationFn: (next: DunningStep[]) => onSave(next),
    onSuccess: () => {
      toast.success("Política salva. Vale para as próximas faturas emitidas.")
      onClose()
    },
    // The server re-validates: ordered days, at most one "dar por perdida" and
    // por último, nada de restringir acesso antes do vencimento.
    onError: error => toast.error(messageFor(error)),
  })

  return (
    <Modal
      open
      onClose={onClose}
      title="Política de cobrança"
      description="Cada passo é um dia relativo ao vencimento. Vale para faturas emitidas daqui em diante — as que já estão sendo cobradas seguem a política com que foram emitidas."
      cancelLabel="Cancelar"
      submitLabel="Salvar política"
      loading={save.isPending}
      onSubmit={() => save.mutate(steps)}
      size="lg"
    >
      <div className="space-y-4">
        <ul className="space-y-2">
          {steps.map((step, i) => (
            <li key={i} className="flex flex-wrap items-center gap-2">
              <Input
                aria-label={`Dia do passo ${i + 1}`}
                inputMode="numeric"
                value={String(step.offset)}
                onChange={event =>
                  setSteps(replace(steps, i, {...step, offset: Number(event.target.value) || 0}))
                }
                className="w-24"
              />
              <select
                aria-label={`Ação do passo ${i + 1}`}
                value={step.action}
                onChange={event =>
                  setSteps(replace(steps, i, {...step, action: event.target.value as DunningAction}))
                }
                className="h-9 min-w-52 rounded-lg border border-border bg-background px-2 text-sm text-foreground"
              >
                {(Object.keys(ACTION_LABEL) as DunningAction[]).map(action => (
                  <option key={action} value={action}>{ACTION_LABEL[action]}</option>
                ))}
              </select>
              <span className="text-xs text-muted-foreground">{dayLabel(step.offset)}</span>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Remover passo ${i + 1}`}
                onClick={() => setSteps(steps.filter((_, j) => j !== i))}
              >
                <X aria-hidden className="size-4"/>
              </Button>
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
            Adicionar passo
          </Button>
          {/* Clearing is not "disable dunning" and must not read as it: an
              invoice that is never chased and never written off sits em aberto
              para sempre parecendo receita. */}
          <Button variant="ghost" size="sm" onClick={() => setSteps([])}>
            Voltar a herdar ({inheritLabel})
          </Button>
        </div>

        {steps.length === 0 && (
          <p className="text-sm text-muted-foreground">
            Sem passos próprios: as faturas vão seguir {inheritLabel}.
          </p>
        )}
      </div>
    </Modal>
  )
}

function replace(steps: DunningStep[], index: number, step: DunningStep): DunningStep[] {
  return steps.map((current, i) => (i === index ? step : current))
}
