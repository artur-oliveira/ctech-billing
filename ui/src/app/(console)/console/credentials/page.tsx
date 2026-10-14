"use client";

import {Badge, Button, buttonVariants, cn, EmptyState, Field, Input, Modal, PageHeader, Skeleton} from "@aoctech/ui";
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query";
import {ExternalLink} from "lucide-react";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {messageFor} from "@/lib/api/client";
import {connectCredential, consoleKeys, disconnectCredential, listConsoleCredentials} from "@/lib/api/console";
import type {ConsoleCredential} from "@/lib/api/consoleTypes";
import {useAddsRefused} from "@/lib/console/addsRefused";
import {developerClientURL} from "@/lib/console/developerLink";
import {useConsoleScope} from "@/lib/console/scope";
import {instantShortDate} from "@/lib/format";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";
import limits from "@/lib/limits.json";

/**
 * The merchant's API credentials (spec § 5.1). A credential is the merchant's
 * own ctech-account OAuth client, connected by id: billing never sees its
 * secret. Provisioned ones (tenant zero's CTech services) are listed, never
 * removable. A suspended live tenant may disconnect, never connect.
 */
export default function ConsoleCredentialsPage() {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  useDocumentTitle(t("console.credentials.title"));
  const addsRefused = useAddsRefused();
  const query = useQuery({queryKey: consoleKeys.credentials(scope), queryFn: () => listConsoleCredentials(scope)});
  const [leaving, setLeaving] = useState<ConsoleCredential | null>(null);
  const creds = query.data?.data ?? [];

  return (
    <div className="space-y-8">
      <PageHeader title={t("console.credentials.title")} description={t("console.credentials.intro")}/>

      {addsRefused
        ? <p className="max-w-prose text-sm text-muted-foreground">{t("errors.code.tenant_suspended")}</p>
        : <ConnectBox scopes={query.data?.machine_scopes ?? []} ready={query.isSuccess}/>}

      <section aria-labelledby="creds-list" className="space-y-3">
        <h2 id="creds-list" className="text-sm font-semibold text-foreground">{t("console.credentials.listTitle")}</h2>
        {query.isPending && (
          <div className="space-y-2" aria-busy>{[0, 1].map(i => <Skeleton key={i} className="h-14 w-full"/>)}</div>
        )}
        {query.isError && <ErrorBlock error={query.error} onRetry={() => void query.refetch()}/>}
        {query.isSuccess && creds.length === 0 && (
          <EmptyState title={t("console.credentials.empty")} className="max-sm:items-start max-sm:text-left"/>
        )}
        {creds.length > 0 && (
          <ul className="divide-y divide-border border-y border-border">
            {creds.map(c => <CredentialRow key={c.client_id} credential={c} onDisconnect={() => setLeaving(c)}/>)}
          </ul>
        )}
      </section>

      <DisconnectDialog credential={leaving} onClose={() => setLeaving(null)}/>
    </div>
  );
}

function CredentialRow({credential: c, onDisconnect}: {credential: ConsoleCredential; onDisconnect: () => void}) {
  const {t} = useTranslation();
  const date = c.connected_at ? instantShortDate(c.connected_at) : "—";
  const meta = c.provisioned
    ? t("console.credentials.provisioned")
    : t(c.connected_by_me ? "console.credentials.byMe" : "console.credentials.byOther", {date});
  return (
    <li className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:gap-4">
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <p className="font-mono text-sm break-all text-foreground">{c.client_id}</p>
          {!c.active && <Badge tone="attention">{t("console.credentials.inactive")}</Badge>}
        </div>
        {c.description && <p className="text-xs text-foreground">{c.description}</p>}
        <p className="text-xs text-muted-foreground">{meta}</p>
      </div>
      {!c.provisioned && (
        <Button variant="ghost" className="min-h-11 self-start text-danger sm:min-h-0 sm:self-auto" onClick={onDisconnect}>
          {t("console.credentials.disconnect")}
        </Button>
      )}
    </li>
  );
}

/** The two steps: create the client at CTech Account, then paste its id here. */
function ConnectBox({scopes, ready}: {scopes: string[]; ready: boolean}) {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  const queryClient = useQueryClient();
  const [clientId, setClientId] = useState("");
  const connect = useMutation({
    mutationFn: (id: string) => connectCredential(scope, id),
    onSuccess: () => {
      toast.success(t("console.credentials.connected"));
      setClientId("");
      void queryClient.invalidateQueries({queryKey: consoleKeys.credentials(scope)});
    },
  });
  const trimmed = clientId.trim();

  return (
    <section aria-labelledby="connect-title" className="space-y-4 rounded-lg bg-surface p-4 sm:p-5">
      <h2 id="connect-title" className="text-sm font-semibold text-foreground">{t("console.credentials.connectTitle")}</h2>
      <ol className="grid gap-5 sm:grid-cols-2 sm:gap-6">
        <li className="flex gap-3">
          <Step n={1}/>
          <div className="min-w-0 flex-1 space-y-3">
            <p className="text-sm text-foreground">{t("console.credentials.step1")}</p>
            {ready ? (
              <a
                href={developerClientURL(scopes)} target="_blank" rel="noopener noreferrer"
                className={cn(buttonVariants({variant: "outline"}), "h-auto min-h-11 w-full gap-1.5 py-2 whitespace-normal sm:min-h-0 sm:w-auto")}
              >
                {t("console.credentials.createClient")}
                <ExternalLink aria-hidden className="size-3.5"/>
              </a>
            ) : <Skeleton className="h-9 w-full sm:w-64"/>}
            {scopes.length > 0 && (
              <details className="group text-xs text-muted-foreground">
                <summary className="cursor-pointer select-none py-1 hover:text-foreground">{t("console.credentials.scopesSummary")}</summary>
                <ul className="mt-2 space-y-0.5 font-mono text-foreground">{scopes.map(s => <li key={s}>{s}</li>)}</ul>
              </details>
            )}
          </div>
        </li>
        <li className="flex gap-3">
          <Step n={2}/>
          <div className="min-w-0 flex-1 space-y-3">
            <p className="text-sm text-foreground">{t("console.credentials.step2")}</p>
            <form
              className="flex flex-col gap-2 sm:flex-row sm:items-start"
              onSubmit={e => { e.preventDefault(); if (trimmed) connect.mutate(trimmed); }}
            >
              <Field
                label={t("console.credentials.clientId")} htmlFor="client-id" className="min-w-0 flex-1"
                error={connect.isError ? messageFor(connect.error) : undefined}
              >
                <Input
                  id="client-id" value={clientId} maxLength={limits.text.clientID} autoComplete="off" spellCheck={false}
                  autoCapitalize="off" className="font-mono"
                  aria-invalid={connect.isError || undefined}
                  aria-describedby={connect.isError ? "client-id-error" : undefined}
                  onChange={e => { setClientId(e.target.value); connect.reset(); }}
                />
              </Field>
              <Button type="submit" variant="brand" className="w-full sm:mt-6 sm:w-auto" disabled={!trimmed || connect.isPending}>
                {t("console.credentials.connect")}
              </Button>
            </form>
          </div>
        </li>
      </ol>
    </section>
  );
}

/** The step's number: the order is the instruction (create first, then paste). */
function Step({n}: {n: number}) {
  return (
    <span aria-hidden className="mt-px flex size-6 shrink-0 items-center justify-center rounded-full border border-border bg-background text-xs font-semibold tabular-nums text-foreground">
      {n}
    </span>
  );
}

function DisconnectDialog({credential, onClose}: {credential: ConsoleCredential | null; onClose: () => void}) {
  const {t, i18n} = useTranslation();
  const scope = useConsoleScope();
  const queryClient = useQueryClient();
  const disconnect = useMutation({
    mutationFn: (id: string) => disconnectCredential(scope, id),
    onSuccess: () => {
      toast.success(t("console.credentials.disconnected"));
      void queryClient.invalidateQueries({queryKey: consoleKeys.credentials(scope)});
      onClose();
    },
    onError: e => toast.error(messageFor(e)),
  });
  return (
    <Modal
      open={credential !== null} onClose={onClose} danger locale={i18n.language}
      title={t("console.credentials.disconnectTitle")}
      submitLabel={t("console.credentials.disconnect")} cancelLabel={t("console.credentials.back")}
      loading={disconnect.isPending}
      onSubmit={() => { if (credential) disconnect.mutate(credential.client_id); }}
    >
      <p className="text-sm [overflow-wrap:anywhere]">{t("console.credentials.disconnectBody", {id: credential?.client_id ?? ""})}</p>
    </Modal>
  );
}
