"use client";

import {Badge, Button, Checkbox, EmptyState, Field, Input, Modal, PageHeader, RowMenu, Skeleton, SwipeRow, Switch} from "@aoctech/ui";
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {SecretReveal} from "@/components/console/SecretReveal";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {messageFor} from "@/lib/api/client";
import {consoleKeys, createWebhook, deleteWebhook, listConsoleWebhooks, rotateWebhookSecret, testWebhook} from "@/lib/api/console";
import type {ConsoleWebhookEndpoint} from "@/lib/api/consoleTypes";
import {useAddsRefused} from "@/lib/console/addsRefused";
import {useConsoleScope} from "@/lib/console/scope";
import {instantShortDate} from "@/lib/format";
import {useDocumentTitle} from "@/lib/hooks/useDocumentTitle";
import {useCreateRequest} from "@/lib/createRequest";
import limits from "@/lib/limits.json";
import {useFieldErrors} from "@/lib/useFieldErrors";

type Pending = {kind: "rotate" | "delete"; endpoint: ConsoleWebhookEndpoint} | null

/**
 * The merchant's webhook endpoints (spec § 5.2). The signing secret appears
 * once, on create and on rotate, in SecretReveal; no list carries it.
 * Endpoints from the tenant plan are listed with no action. A suspended live
 * tenant may rotate and delete, never add or test (plan scope decision 1).
 */
export default function ConsoleWebhooksPage() {
  const {t, i18n} = useTranslation();
  const scope = useConsoleScope();
  useDocumentTitle(t("console.webhooks.title"));
  const queryClient = useQueryClient();
  const addsRefused = useAddsRefused();
  // After "Testar" the result arrives with the next delivery pass (up to a
  // minute), so the list follows itself for a couple of minutes and then stops.
  const [testedAt, setTestedAt] = useState<number | null>(null);
  const query = useQuery({
    queryKey: consoleKeys.webhooks(scope),
    queryFn: () => listConsoleWebhooks(scope),
    refetchInterval: () => (testedAt !== null && Date.now() - testedAt < 120_000 ? 8_000 : false),
  });
  const [creating, setCreating] = useState(false);
  const [asked, setAsked] = useState(false);
  useCreateRequest("webhook", () => setAsked(true));
  const [pending, setPending] = useState<Pending>(null);
  const [secret, setSecret] = useState<string | null>(null);
  const refresh = () => queryClient.invalidateQueries({queryKey: consoleKeys.webhooks(scope)});

  const endpoints = query.data?.data ?? [];
  const max = query.data?.max_endpoints ?? limits.maxWebhookEndpoints;
  const atLimit = endpoints.length >= max;
  const canAdd = query.isSuccess && !addsRefused && !atLimit;
  // The search can ask for the form before the list is known. The ask is
  // answered once, when the list settles: opened where the button itself
  // could be pressed, dropped where it could not, so freeing a slot later
  // never opens a form nobody is asking for now.
  const settled = query.isSuccess || query.isError;
  if (asked && settled) {
    setAsked(false);
    if (canAdd) setCreating(true);
  }

  const test = useMutation({
    mutationFn: (id: string) => testWebhook(scope, id),
    onSuccess: () => { setTestedAt(Date.now()); toast.success(t("console.webhooks.tested")); },
    onError: e => toast.error(messageFor(e)),
  });
  const act = useMutation({
    mutationFn: async (p: NonNullable<Pending>) =>
      p.kind === "rotate" ? (await rotateWebhookSecret(scope, p.endpoint.id)).secret : (await deleteWebhook(scope, p.endpoint.id), null),
    onSuccess: (newSecret, p) => {
      setPending(null);
      void refresh();
      if (p.kind === "rotate") setSecret(newSecret);
      else toast.success(t("console.webhooks.deleted"));
    },
    onError: e => toast.error(messageFor(e)),
  });

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("console.webhooks.title")}
        description={t("console.webhooks.intro")}
        // Offered once the list is known: the limit decides whether it can be pressed.
        action={addsRefused || !query.isSuccess ? undefined : (
          <Button variant="brand" onClick={() => setCreating(true)} disabled={atLimit} className="w-full sm:w-auto">
            {t("console.webhooks.new")}
          </Button>
        )}
      />
      {addsRefused && <p className="max-w-prose text-sm text-muted-foreground">{t("errors.code.tenant_suspended")}</p>}

      <section aria-labelledby="hooks-list" className="space-y-3">
        <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
          <h2 id="hooks-list" className="text-sm font-semibold text-foreground">{t("console.webhooks.listTitle")}</h2>
          {query.isSuccess && (
            <p className="text-xs text-muted-foreground tabular-nums" aria-live="polite">
              {atLimit ? t("console.webhooks.atLimit", {max}) : t("console.webhooks.count", {count: endpoints.length, max})}
            </p>
          )}
        </div>
        {query.isPending && <div className="space-y-2" aria-busy>{[0, 1].map(i => <Skeleton key={i} className="h-16 w-full"/>)}</div>}
        {query.isError && <ErrorBlock error={query.error} onRetry={() => void query.refetch()}/>}
        {query.isSuccess && endpoints.length === 0 && (
          <EmptyState title={t("console.webhooks.empty")} description={t("console.webhooks.emptyHint")} className="max-sm:items-start max-sm:text-left"/>
        )}
        {endpoints.length > 0 && (
          <ul className="divide-y divide-border border-y border-border">
            {endpoints.map(e => (
              <EndpointRow
                key={e.id} endpoint={e} canTest={!addsRefused} testing={test.isPending && test.variables === e.id}
                onTest={() => test.mutate(e.id)}
                onRotate={() => setPending({kind: "rotate", endpoint: e})}
                onDelete={() => setPending({kind: "delete", endpoint: e})}
              />
            ))}
          </ul>
        )}
      </section>

      <NewEndpointDialog
        open={creating} eventTypes={query.data?.event_types ?? []} onClose={() => setCreating(false)}
        onCreated={s => { setCreating(false); setSecret(s); void refresh(); }}
      />
      <Modal
        open={pending !== null} onClose={() => setPending(null)} danger locale={i18n.language}
        title={pending ? t(pending.kind === "rotate" ? "console.webhooks.rotateTitle" : "console.webhooks.deleteTitle", {url: pending.endpoint.url}) : ""}
        submitLabel={t(pending?.kind === "rotate" ? "console.webhooks.rotate" : "console.webhooks.delete")}
        cancelLabel={t("console.webhooks.back")} loading={act.isPending}
        onSubmit={() => { if (pending) act.mutate(pending); }}
      >
        <p className="text-sm">{t(pending?.kind === "rotate" ? "console.webhooks.rotateBody" : "console.webhooks.deleteBody")}</p>
      </Modal>
      <SecretReveal secret={secret} onDone={() => setSecret(null)}/>
    </div>
  );
}

/**
 * One endpoint. From `sm` a single line with the actions inline. On a phone
 * the URL, its meta and status stack; Testar stays on the row, and Trocar
 * segredo / Excluir are the left swipe and the "⋯" (DESIGN.md "On a phone").
 */
function EndpointRow({endpoint: e, canTest, testing, onTest, onRotate, onDelete}: {
  endpoint: ConsoleWebhookEndpoint; canTest: boolean; testing: boolean; onTest: () => void; onRotate: () => void; onDelete: () => void
}) {
  const {t, i18n} = useTranslation();
  const testable = canTest && !e.provisioned && e.status === "active";
  const more = e.provisioned ? [] : [
    {key: "rotate", label: t("console.webhooks.rotate"), onSelect: onRotate},
    {key: "delete", label: t("console.webhooks.delete"), onSelect: onDelete, destructive: true},
  ];
  const meta = [
    e.events.length === 0 ? t("console.webhooks.allEvents") : t("console.webhooks.someEvents", {count: e.events.length}),
    e.provisioned ? t("console.webhooks.provisioned") : e.added_at ? t("console.webhooks.addedOn", {date: instantShortDate(e.added_at)}) : null,
  ].filter((m): m is string => m !== null);
  const active = e.status === "active";
  // What the endpoint last answered; a provisioned one is CTech's own and is
  // not described here.
  const health = e.provisioned || !active ? null
    : !e.last_delivery_at ? {tone: "text-muted-foreground", text: t("console.webhooks.untested")}
    : e.last_delivery_ok ? {tone: "text-success", text: t("console.webhooks.responding")}
    : {tone: "text-danger", text: t("console.webhooks.failing", {detail: e.last_delivery_detail ?? ""})};

  return (
    <li>
      <SwipeRow actions={more} enabled={more.length > 0} frontClassName="py-3">
        <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 gap-y-2 sm:flex sm:items-center sm:gap-4">
          <div className="min-w-0 space-y-1 sm:flex-1">
            <p className="font-mono text-sm [overflow-wrap:anywhere] text-foreground">{e.url}</p>
            <p className="text-xs text-muted-foreground">
              {meta.map((m, i) => <span key={m}>{i > 0 && <span aria-hidden> • </span>}<span>{m}</span></span>)}
            </p>
            {health && <p className={`text-xs ${health.tone}`}>{health.text}</p>}
            {!active && <p className="text-xs text-danger">{t("console.webhooks.disabledHint")}</p>}
          </div>
          <div className="flex items-center gap-1 sm:contents">
            <Badge tone={active ? "neutral" : "urgent"} className="sm:order-none">
              {t(active ? "console.webhooks.active" : "console.webhooks.disabled")}
            </Badge>
            {more.length > 0 && (
              <RowMenu className="-my-1.5 sm:hidden" locale={i18n.language} label={t("console.webhooks.more", {url: e.url})} items={more}/>
            )}
          </div>
          {!e.provisioned && (testable || more.length > 0) && (
            <div className="col-span-full flex flex-wrap items-center gap-2 sm:col-auto sm:shrink-0">
              {testable && (
                <Button variant="outline" size="sm" className="max-sm:min-h-11 max-sm:px-4" disabled={testing} onClick={onTest}>
                  {t("console.webhooks.test")}
                </Button>
              )}
              {more.map(a => (
                <Button key={a.key} size="sm" variant="ghost" className={a.destructive ? "text-danger max-sm:hidden" : "max-sm:hidden"} onClick={a.onSelect}>
                  {a.label}
                </Button>
              ))}
            </div>
          )}
        </div>
      </SwipeRow>
    </li>
  );
}

/** Groups "invoice.paid" under "invoice", keeping the catalogue's order. */
function groupEvents(types: string[]): [string, string[]][] {
  const groups = new Map<string, string[]>();
  for (const ev of types) {
    const g = ev.split(".")[0];
    groups.set(g, [...(groups.get(g) ?? []), ev]);
  }
  return [...groups];
}

function NewEndpointDialog({open, eventTypes, onClose, onCreated}: {
  open: boolean; eventTypes: string[]; onClose: () => void; onCreated: (secret: string) => void
}) {
  const {t, i18n} = useTranslation();
  const scope = useConsoleScope();
  const [url, setUrl] = useState("");
  const [all, setAll] = useState(true);
  const [chosen, setChosen] = useState<string[]>([]);
  const fe = useFieldErrors(["url", "events"]);
  const create = useMutation({
    mutationFn: () => createWebhook(scope, {url: url.trim(), events: all ? [] : chosen}),
    onSuccess: res => { setUrl(""); setAll(true); setChosen([]); onCreated(res.secret); },
    onError: e => { fe.set(e); },
  });
  const close = () => { fe.reset(); create.reset(); onClose(); };

  return (
    <Modal
      open={open} onClose={close} locale={i18n.language} title={t("console.webhooks.form.title")}
      submitLabel={t("console.webhooks.form.submit")} cancelLabel={t("console.webhooks.back")}
      submitDisabled={url.trim() === "" || (!all && chosen.length === 0)} loading={create.isPending}
      onSubmit={() => { fe.reset(); create.mutate(); }}
    >
      <div className="space-y-5">
        <Field label={t("console.webhooks.form.url")} htmlFor="hook-url" hint={t("console.webhooks.form.urlHint")} error={fe.of("url")}>
          <Input
            id="hook-url" type="url" inputMode="url" autoComplete="off" spellCheck={false} maxLength={limits.text.webhookURL}
            value={url} placeholder={t("console.webhooks.form.urlPlaceholder")} className="font-mono"
            {...fe.props("url", "hook-url")}
            onChange={e => { setUrl(e.target.value); fe.clear("url"); }}
          />
        </Field>
        <label className="flex min-h-11 items-center gap-3 text-sm text-foreground sm:min-h-8">
          <Switch checked={all} onCheckedChange={setAll}/>
          {t("console.webhooks.form.all")}
        </label>
        {!all && (
          <div className="space-y-4">
            {groupEvents(eventTypes).map(([group, events]) => (
              <fieldset key={group} className="space-y-0.5">
                <legend className="mb-1 text-xs font-semibold text-muted-foreground">
                  {t(`console.webhooks.groups.${group}`, {defaultValue: group})}
                </legend>
                {events.map(ev => (
                  <label key={ev} className="flex min-h-11 cursor-pointer items-center gap-2.5 sm:min-h-8">
                    <Checkbox
                      checked={chosen.includes(ev)}
                      onCheckedChange={on => setChosen(c => on ? [...c, ev] : c.filter(x => x !== ev))}
                    />
                    <span className="font-mono text-xs text-foreground">{ev}</span>
                  </label>
                ))}
              </fieldset>
            ))}
          </div>
        )}
        {(fe.of("events") || fe.general) && (
          <p role="alert" className="text-sm text-danger">{fe.of("events") ?? fe.general}</p>
        )}
      </div>
    </Modal>
  );
}
