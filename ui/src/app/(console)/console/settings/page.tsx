"use client"

import {Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"

import {DunningPolicyCard} from "@/components/console/DunningPolicyCard"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {consoleKeys, getConsoleSettings, setDunningPolicy} from "@/lib/api/console"
import type {DunningStep} from "@/lib/api/consoleTypes"
import {useMode} from "@/lib/console/useMode"

/**
 * C17 — configurações.
 *
 * One thing is editable, and the rest are stated as facts. Numbering has no
 * options (gapless per year), retention is a constant (ADR 0009) and the sender
 * address is a deployment secret — rendering them as disabled fields would be a
 * settings screen lying about what it controls.
 */
export default function ConsoleSettingsPage() {
  const mode = useMode()
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: consoleKeys.settings(mode),
    queryFn: () => getConsoleSettings(mode),
  })

  const save = useMutation({
    mutationFn: (steps: DunningStep[]) => setDunningPolicy(steps, mode),
    onSuccess: () => queryClient.invalidateQueries({queryKey: consoleKeys.settings(mode)}),
  })

  if (query.isPending) return <SettingsSkeleton/>
  if (query.isError) return <ErrorBlock error={query.error} onRetry={query.refetch}/>

  const settings = query.data

  return (
    <div className="space-y-8">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">Configurações</h1>

      <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 text-sm sm:grid-cols-3">
        <Fact label="Organização" value={settings.organization.display_name}/>
        <Fact label="Identificador" value={settings.organization.organization_id}/>
        <Fact
          label="Cobrança"
          value={settings.organization.can_charge ? "Liberada" : "Bloqueada"}
        />
        <Fact label="Numeração de faturas" value={settings.numbering}/>
        <Fact label="Retenção" value={settings.retention}/>
      </dl>

      <DunningPolicyCard
        title="Política de cobrança"
        description="O que acontece com uma fatura que ninguém pagou: quando lembrar, quando restringir o acesso e quando dar por perdida. Não é retentativa de cobrança — PIX é o cliente que paga, o billing não debita ninguém."
        policy={settings.dunning}
        inheritLabel="a política padrão da CTech"
        onSave={steps => save.mutateAsync(steps)}
      />
    </div>
  )
}

function Fact({label, value}: { label: string; value: string }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-foreground">{value}</dd>
    </div>
  )
}

function SettingsSkeleton() {
  return (
    <div className="space-y-8" aria-busy>
      <Skeleton className="h-6 w-48"/>
      <Skeleton className="h-20 w-full"/>
      <Skeleton className="h-32 w-full"/>
    </div>
  )
}
