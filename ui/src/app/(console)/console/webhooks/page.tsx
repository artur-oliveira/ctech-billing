"use client";

import {Badge, Button, EmptyState, Field, Input, Modal, PageHeader, RowMenu, Skeleton, SwipeRow, Switch, Checkbox} from "@aoctech/ui";
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
  // The working endpoints lead; the ones disabled by repeated failures are
  // the same row over and over, so they wait behind a collapsed group.
  const live = endpoints.filter(e => e.status === "active");
  const disabled = endpoints.filter(e => e.status !== "active");
  // Selection is only ever of rows that still exist and that the merchant may delete.
  const [picked, setPicked] = useState<string[]>([]);
  const selected = endpoints.filter(e => !e.provisioned && picked.includes(e.id));
  const toggle = (id: string, on: boolean) => setPicked(p => on ? [...p, id] : p.filter(x => x !== id));
  const [bulkAsked, setBulkAsked] = useState(false);
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

  // Sequential: every delete moves the same revision row, so parallel ones
  // would collide with each other.
  const bulk = useMutation({
    mutationFn: async (ids: string[]) => {
      let failed = 0;
      for (const id of ids) {
        try { await deleteWebhook(scope, id); } catch { failed++; }
      }
      return {failed, total: ids.length};
    },
    onSuccess: ({failed, total}) => {
      setBulkAsked(false);
      setPicked([]);
      void refresh();
      if (failed === 0) toast.success(t("console.webhooks.bulkDeleted", {count: total}));
      else toast.error(t("console.webhooks.bulkPartial", {failed, total}));
    },
  });

  const rowProps = (e: ConsoleWebhookEndpoint) => ({
    endpoint: e, canTest: !addsRefused, testing: test.isPending && test.variables === e.id,
    picked: picked.includes(e.id), onPick: (on: boolean) => toggle(e.id, on),
    onTest: () => test.mutate(e.id),
    onRotate: () => setPending({kind: "rotate", endpoint: e}),
    onDelete: () => setPending({kind: "delete", endpoint: e}),
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
        {atLimit && !addsRefused && (
          <p className="text-xs text-muted-foreground">{t("console.webhooks.limitHint")}</p>
        )}
        {selected.length > 0 && (
          <div className="flex flex-wrap items-center gap-3 rounded-md bg-muted px-3 py-2" role="region" aria-label={t("console.webhooks.bulkBar")}>
            <span className="text-sm text-foreground tabular-nums">{t("console.webhooks.selected", {count: selected.length})}</span>
            <Button size="sm" variant="ghost" onClick={() => setPicked([])}>{t("console.webhooks.clearSelection")}</Button>
            <Button size="sm" variant="outline" className="text-danger" onClick={() => setBulkAsked(true)}>
              {t("console.webhooks.deleteSelected", {count: selected.length})}
            </Button>
          </div>
        )}
        {live.length > 0 && (
          <ul className="divide-y divide-border border-y border-border">
            {live.map(e => <EndpointRow key={e.id} {...rowProps(e)}/>)}
          </ul>
        )}
        {disabled.length > 0 && (
          <details className="group">
            <summary className="flex min-h-11 cursor-pointer items-center gap-2 text-sm font-medium text-foreground sm:min-h-8">
              {t("console.webhooks.disabledGroup", {count: disabled.length})}
            </summary>
            <div className="space-y-2">
              {disabled.some(e => !e.provisioned) && (
                <label className="flex min-h-11 items-center gap-2.5 text-xs text-muted-foreground sm:min-h-8">
                  <Checkbox
                    checked={disabled.filter(e => !e.provisioned).every(e => picked.includes(e.id))}
                    onCheckedChange={on => setPicked(p => {
                      const ids = disabled.filter(e => !e.provisioned).map(e => e.id);
                      return on ? [...new Set([...p, ...ids])] : p.filter(x => !ids.includes(x));
                    })}
                  />
                  {t("console.webhooks.selectDisabled")}
                </label>
              )}
              <ul className="divide-y divide-border border-y border-border">
                {disabled.map(e => <EndpointRow key={e.id} {...rowProps(e)}/>)}
              </ul>
            </div>
          </details>
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
      <Modal
        open={bulkAsked} onClose={() => setBulkAsked(false)} danger locale={i18n.language}
        title={t("console.webhooks.bulkTitle", {count: selected.length})}
        submitLabel={t("console.webhooks.delete")} cancelLabel={t("console.webhooks.back")} loading={bulk.isPending}
        onSubmit={() => bulk.mutate(selected.map(e => e.id))}
      >
        <p className="text-sm">{t("console.webhooks.bulkBody")}</p>
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
function EndpointRow({endpoint: e, canTest, testing, picked, onPick, onTest, onRotate, onDelete}: {
  endpoint: ConsoleWebhookEndpoint; canTest: boolean; testing: boolean; picked: boolean; onPick: (on: boolean) => void
  onTest: () => void; onRotate: () => void; onDelete: () => void
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
  const health = e.provisioned ? null
    : !active ? (e.last_delivery_at && !e.last_delivery_ok
      ? {tone: "text-danger", text: t("console.webhooks.lastFailure", {detail: e.last_delivery_detail ?? "", date: instantShortDate(e.last_delivery_at)})}
      : null)
    : !e.last_delivery_at ? {tone: "text-muted-foreground", text: t("console.webhooks.untested")}
    : e.last_delivery_ok ? {tone: "text-success", text: t("console.webhooks.responding")}
    : {tone: "text-danger", text: t("console.webhooks.failing", {detail: e.last_delivery_detail ?? ""})};

  return (
    <li>
      <SwipeRow actions={more} enabled={more.length > 0} frontClassName="py-3">
        <div className="flex items-start gap-3">
        {/* Provisioned rows cannot be deleted, so they have no box; the slot keeps the URLs aligned. */}
        <div className="flex min-h-5 w-4 shrink-0 items-center pt-0.5">
          {!e.provisioned && <Checkbox checked={picked} onCheckedChange={onPick} aria-label={t("console.webhooks.select", {url: e.url})}/>}
        </div>
        <div className="min-w-0 flex-1 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 gap-y-2 sm:flex sm:items-center sm:gap-4">
          <div className="min-w-0 space-y-1 sm:flex-1">
            <p className="font-mono text-sm [overflow-wrap:anywhere] text-foreground">{e.url}</p>
            <p className="text-xs text-muted-foreground">
              {meta.map((m, i) => <span key={m}>{i > 0 && <span aria-hidden> • </span>}<span>{m}</span></span>)}
            </p>
            {health && <p className={`text-xs ${health.tone}`}>{health.text}</p>}
            {!active && <p className="text-xs text-danger">{t(e.provisioned ? "console.webhooks.disabledManagedHint" : "console.webhooks.disabledHint")}</p>}
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
