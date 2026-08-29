"use client"

import {Alert, Button, Field, Input, Modal, Skeleton} from "@aoctech/ui"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {useState} from "react"
import {toast} from "sonner"

import {DunningPolicyCard} from "@/components/console/DunningPolicyCard"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor} from "@/lib/api/client"
import {
  consoleKeys,
  getConsoleSettings,
  setDunningPolicy,
  setIssuer,
  type IssuerInput,
} from "@/lib/api/console"
import type {DunningStep, Issuer} from "@/lib/api/consoleTypes"
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

      {settings.documents_enabled && (
        <IssuerCard issuer={settings.issuer} mode={mode}/>
      )}

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

/**
 * The issuer block — what the invoice PDF is headed by.
 *
 * An empty legal name is called out rather than left blank, because the
 * consequence is invisible from every screen: documents go out headed by the
 * display name, which is a brand and not a company. Nobody discovers that until
 * an accountant asks.
 *
 * Nothing here is validated beyond length. Billing is not the authority on a
 * CNPJ or an address, and refusing a legitimate one because its own check was
 * wrong would be worse than printing what it was told.
 */
function IssuerCard({issuer, mode}: { issuer: Issuer; mode: "live" | "test" }) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)

  return (
    <section aria-labelledby="emissor" className="space-y-3">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 id="emissor" className="text-sm font-medium text-foreground">Emissor</h2>
          <p className="max-w-prose text-sm text-muted-foreground">
            O cabeçalho do PDF da fatura: quem está cobrando. Vale para os documentos gerados
            daqui em diante.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
          Editar emissor
        </Button>
      </div>

      {!issuer.legal_name && (
        <Alert tone="warning" title="As faturas saem sem razão social">
          O PDF é encabeçado pelo nome de exibição da organização — que é uma marca, não uma
          empresa. Um contador vai pedir a razão social e o CNPJ.
        </Alert>
      )}

      <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 text-sm sm:grid-cols-2">
        <Fact label="Razão social" value={issuer.legal_name || "—"}/>
        <Fact label="CNPJ" value={issuer.tax_id || "—"}/>
        <Fact label="Endereço" value={issuer.address || "—"}/>
        <Fact label="E-mail no documento" value={issuer.email || "—"}/>
      </dl>

      {editing && (
        <IssuerEditor
          issuer={issuer}
          mode={mode}
          onClose={() => setEditing(false)}
          onSaved={() => {
            void queryClient.invalidateQueries({queryKey: consoleKeys.settings(mode)})
            setEditing(false)
          }}
        />
      )}
    </section>
  )
}

function IssuerEditor({
  issuer,
  mode,
  onClose,
  onSaved,
}: {
  issuer: Issuer
  mode: "live" | "test"
  onClose: () => void
  onSaved: () => void
}) {
  const [form, setForm] = useState<IssuerInput>({
    legal_name: issuer.legal_name ?? "",
    tax_id: issuer.tax_id ?? "",
    address: issuer.address ?? "",
    email: issuer.email ?? "",
  })

  const save = useMutation({
    mutationFn: () => setIssuer(form, mode),
    onSuccess: () => {
      toast.success("Emissor salvo.")
      onSaved()
    },
    onError: error => toast.error(messageFor(error)),
  })

  const field = (key: keyof IssuerInput) => ({
    value: form[key],
    onChange: (event: React.ChangeEvent<HTMLInputElement>) =>
      setForm({...form, [key]: event.target.value}),
  })

  return (
    <Modal
      open
      onClose={onClose}
      title="Emissor do documento"
      description="Sai no cabeçalho de cada fatura em PDF. Documentos já gerados não mudam — eles foram entregues como estavam."
      cancelLabel="Cancelar"
      submitLabel="Salvar emissor"
      loading={save.isPending}
      onSubmit={() => save.mutate()}
    >
      <div className="space-y-4">
        <Field label="Razão social" htmlFor="issuer-legal-name">
          <Input id="issuer-legal-name" placeholder="A O CARVALHO TECH LTDA" {...field("legal_name")}/>
        </Field>
        <Field label="CNPJ" htmlFor="issuer-tax-id">
          <Input id="issuer-tax-id" placeholder="12.345.678/0001-90" {...field("tax_id")}/>
        </Field>
        <Field label="Endereço" htmlFor="issuer-address">
          <Input id="issuer-address" placeholder="Rua Exemplo, 100 — São Paulo/SP" {...field("address")}/>
        </Field>
        <Field
          label="E-mail no documento"
          htmlFor="issuer-email"
          hint="Para onde o cliente escreve sobre a cobrança. Não é o remetente dos lembretes."
        >
          <Input id="issuer-email" placeholder="cobranca@exemplo.com.br" {...field("email")}/>
        </Field>
      </div>
    </Modal>
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
