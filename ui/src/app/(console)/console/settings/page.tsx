"use client"

import limits from "@/lib/limits.json"
import {Alert, Button, Field, Input, Skeleton} from "@aoctech/ui"
import {Modal} from "@/components/ui/ConsoleOverlay"
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {DunningPolicyCard} from "@/components/console/DunningPolicyCard"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {messageFor} from "@/lib/api/client"
import {useFieldErrors} from "@/lib/useFieldErrors"
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
  const {t} = useTranslation()
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

  /** An enum code from the API, translated; an unknown one shows as itself. */
  const settingValue = (group: "numberingValue" | "retentionValue", code: string) =>
    t(`console.settings.${group}.${code}`, {defaultValue: code})

  if (query.isPending) return <SettingsSkeleton/>
  if (query.isError) return <ErrorBlock error={query.error} onRetry={query.refetch}/>

  const settings = query.data

  return (
    <div className="space-y-8">
      <h1 className="text-lg font-semibold tracking-[-0.01em] text-foreground">{t("console.settings.title")}</h1>

      <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 text-sm sm:grid-cols-3">
        <Fact label={t("console.settings.organization")} value={settings.organization.display_name}/>
        <Fact label={t("console.settings.identifier")} value={settings.organization.organization_id}/>
        <Fact
          label={t("console.settings.billing")}
          value={settings.organization.can_charge ? t("console.settings.billingOn") : t("console.settings.billingOff")}
        />
        <Fact label={t("console.settings.numbering")} value={settingValue("numberingValue", settings.numbering)}/>
        <Fact label={t("console.settings.retention")} value={settingValue("retentionValue", settings.retention)}/>
      </dl>

      {settings.documents_enabled && (
        <IssuerCard issuer={settings.issuer} mode={mode}/>
      )}

      <DunningPolicyCard
        title={t("console.settings.dunningTitle")}
        description={t("console.settings.dunningDescription")}
        policy={settings.dunning}
        inheritLabel={t("console.settings.inherit")}
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
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)

  return (
    <section aria-labelledby="emissor" className="space-y-3">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 id="emissor" className="text-sm font-medium text-foreground">{t("console.settings.issuer")}</h2>
          <p className="max-w-prose text-sm text-muted-foreground">{t("console.settings.issuerNote")}</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
          {t("console.settings.editIssuer")}
        </Button>
      </div>

      {!issuer.legal_name && (
        <Alert tone="warning" title={t("console.settings.noLegalTitle")}>
          {t("console.settings.noLegalBody")}
        </Alert>
      )}

      <dl className="grid gap-x-6 gap-y-4 border-y border-border py-4 text-sm sm:grid-cols-2">
        <Fact label={t("console.settings.legalName")} value={issuer.legal_name || "—"}/>
        <Fact label={t("console.settings.taxId")} value={issuer.tax_id || "—"}/>
        <Fact label={t("console.settings.address")} value={issuer.address || "—"}/>
        <Fact label={t("console.settings.email")} value={issuer.email || "—"}/>
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
  const {t} = useTranslation()
  const [form, setForm] = useState<IssuerInput>({
    legal_name: issuer.legal_name ?? "",
    tax_id: issuer.tax_id ?? "",
    address: issuer.address ?? "",
    email: issuer.email ?? "",
  })

  const fe = useFieldErrors(["legal_name", "tax_id", "address", "email"], {keepOthers: false})
  const save = useMutation({
    mutationFn: () => setIssuer(form, mode),
    onSuccess: () => {
      toast.success(t("console.settings.saved"))
      onSaved()
    },
    onError: error => { if (!fe.set(error)) toast.error(messageFor(error)) },
  })

  const field = (key: keyof IssuerInput, id: string) => ({
    value: form[key],
    onChange: (event: React.ChangeEvent<HTMLInputElement>) => {
      setForm({...form, [key]: event.target.value})
      fe.clear(key)
    },
    ...fe.props(key, id),
  })

  return (
    <Modal
      open
      onClose={onClose}
      title={t("console.settings.issuerTitle")}
      description={t("console.settings.issuerBody")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.settings.save")}
      loading={save.isPending}
      onSubmit={() => { fe.reset(); save.mutate() }}
    >
      <div className="space-y-4">
        <Field label={t("console.settings.legalName")} htmlFor="issuer-legal-name" error={fe.of("legal_name")}>
          <Input id="issuer-legal-name" maxLength={limits.text.legalName} placeholder="A O CARVALHO TECH LTDA" {...field("legal_name", "issuer-legal-name")}/>
        </Field>
        <Field label={t("console.settings.taxId")} htmlFor="issuer-tax-id" error={fe.of("tax_id")}>
          <Input id="issuer-tax-id" maxLength={limits.text.taxID} placeholder="12.345.678/0001-90" {...field("tax_id", "issuer-tax-id")}/>
        </Field>
        <Field label={t("console.settings.address")} htmlFor="issuer-address" error={fe.of("address")}>
          <Input id="issuer-address" maxLength={limits.text.address} placeholder="Rua Exemplo, 100 • São Paulo/SP" {...field("address", "issuer-address")}/>
        </Field>
        <Field
          label={t("console.settings.email")}
          htmlFor="issuer-email"
          error={fe.of("email")}
          hint={t("console.settings.emailHint")}
        >
          <Input id="issuer-email" type="email" maxLength={limits.text.email} placeholder="cobranca@exemplo.com.br" {...field("email", "issuer-email")}/>
        </Field>
        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}
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
