"use client"

import {Button, Steps, type StepItem, type StepState} from "@aoctech/ui"
import {useMutation, useQueryClient} from "@tanstack/react-query"
import {Check, Copy} from "lucide-react"
import {useState} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {connect, connectionKeys, setOnboarding} from "@/lib/api/connections"
import {messageFor} from "@/lib/api/client"
import {ONBOARDING_STEPS, type Connection, type ConnectForm as ConnectFormValues, type ConnectionsScope, type OnboardingStep, type ProviderInfo} from "@/lib/api/connectionsTypes"
import {useIntent} from "@/lib/api/idempotency"
import {instantShortDate} from "@/lib/format"

import {ConnectForm} from "./ConnectForm"

/**
 * The guided onboarding for a live Inter connection (BYOK spec § 11.1): six
 * steps, one open at a time, where the person stopped saved on the server so a
 * week of waiting for Inter's validation resumes at the right place.
 */
export function InterOnboarding({scope, connection, providers}: {scope: ConnectionsScope; connection: Connection | null; providers: ProviderInfo[]}) {
  const {t} = useTranslation()
  const qc = useQueryClient()
  const saved: OnboardingStep = connection?.onboarding_step && connection.onboarding_step !== "webhook" ? connection.onboarding_step : "requirements"
  const [current, setCurrent] = useState<OnboardingStep>(saved)
  const intent = useIntent()
  const save = useMutation({
    mutationFn: (step: OnboardingStep) => setOnboarding(scope, "inter", step, crypto.randomUUID()),
    onSuccess: () => void qc.invalidateQueries({queryKey: connectionKeys.list(scope)}),
    // The step is display only (spec § 11.1): a failed save never blocks the walk.
    onError: e => toast.error(messageFor(e), {id: "onboarding-save"}),
  })
  const go = (step: OnboardingStep) => {
    setCurrent(step)
    save.mutate(step)
  }
  const doConnect = useMutation({
    mutationFn: (form: ConnectFormValues) => connect(scope, "inter", form, intent.key()),
    onSuccess: c => {
      intent.done()
      qc.setQueryData(connectionKeys.one(scope, "inter"), c)
      void qc.invalidateQueries({queryKey: connectionKeys.list(scope)})
      void qc.invalidateQueries({queryKey: ["finance"]})
      toast.success(t("connections.screen.connected"))
    },
  })

  const scopes = providers.find(p => p.provider === "inter")?.scopes
  const scopeList = [...(scopes?.statement ?? []), ...(scopes?.collection ?? [])].join(" ")
  const waiting = current === "validation" && connection?.onboarding_step === "validation"
  const index = ONBOARDING_STEPS.indexOf(current)
  const stateOf = (step: OnboardingStep, i: number): StepState => {
    if (i < index) return "done"
    if (step === current) return waiting ? "waiting" : "current"
    return "upcoming"
  }
  const next = (step: OnboardingStep) => ONBOARDING_STEPS[ONBOARDING_STEPS.indexOf(step) + 1]
  const nextButton = (step: OnboardingStep) => (
    <Button variant="outline" size="sm" className="max-sm:w-full" onClick={() => go(next(step))}>{t(`connections.steps.${step}.next`)}</Button>
  )

  const content: Record<OnboardingStep, React.ReactNode> = {
    requirements: (
      <div className="space-y-3">
        <p className="text-pretty text-muted-foreground">{t("connections.steps.requirements.body")}</p>
        {nextButton("requirements")}
      </div>
    ),
    create_integration: (
      <div className="space-y-3">
        <p className="text-pretty text-muted-foreground">{t("connections.steps.create_integration.body")}</p>
        <ScopeList text={scopeList}/>
        <p className="font-medium text-foreground">{t("connections.steps.create_integration.noPayments")}</p>
        <p className="text-pretty text-muted-foreground">{t("connections.steps.create_integration.questionnaire")}</p>
        {nextButton("create_integration")}
      </div>
    ),
    validation: (
      <div className="space-y-3">
        <p className="text-pretty text-muted-foreground">{t("connections.steps.validation.body")}</p>
        {waiting && connection?.onboarding_since
          ? <p className="text-foreground">{t("connections.steps.validation.since", {date: instantShortDate(connection.onboarding_since)})}</p>
          : <Button variant="outline" size="sm" className="max-sm:w-full" onClick={() => go("validation")}>{t("connections.steps.validation.waiting")}</Button>}
        <div>{nextButton("validation")}</div>
      </div>
    ),
    download: (
      <div className="space-y-3">
        <p className="text-pretty text-muted-foreground">{t("connections.steps.download.body")}</p>
        {nextButton("download")}
      </div>
    ),
    upload: (
      <div className="pt-1">
        <ConnectForm defaultEmail={connection?.notify_email} onSubmit={async form => { await doConnect.mutateAsync(form) }}/>
      </div>
    ),
    webhook: <p className="text-pretty text-muted-foreground">{t("connections.steps.webhook.body")}</p>,
  }

  const items: StepItem[] = ONBOARDING_STEPS.map((step, i) => ({
    id: step,
    title: t(`connections.steps.${step}.title`),
    state: stateOf(step, i),
    stateLabel: t(`connections.steps.state.${stateOf(step, i)}`),
    content: content[step],
  }))

  return <Steps label={t("connections.steps.label")} items={items} className="max-w-2xl"/>
}

/** The scopes to tick at Inter, as one copyable line. */
function ScopeList({text}: {text: string}) {
  const {t} = useTranslation()
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-surface px-3 py-2">
      <code className="min-w-0 flex-1 break-words font-mono text-xs text-foreground">{text}</code>
      <Button variant="ghost" size="sm" onClick={() => {
        void navigator.clipboard?.writeText(text)
        setCopied(true)
        toast.success(t("connections.steps.create_integration.copied"), {id: "scopes-copied"})
      }}>
        {copied ? <Check aria-hidden/> : <Copy aria-hidden/>}
        {t("connections.steps.create_integration.copy")}
      </Button>
    </div>
  )
}
