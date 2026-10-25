"use client";

import {Button, Field, Modal, Select, Checkbox} from "@aoctech/ui";
import {useQueryClient} from "@tanstack/react-query";
import {useEffect, useRef, useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {classify, connectionKeys} from "@/lib/api/connections";
import type {UnclassifiedLine} from "@/lib/api/connectionsTypes";
import {messageFor} from "@/lib/api/client";
import {financeKeys} from "@/lib/api/finance";
import type {Account} from "@/lib/api/financeTypes";
import type {Intent} from "@/lib/api/idempotency";
import {accountName} from "@/lib/finance/accountName";
import {directionOf, runBulk, type BulkBody, type BulkProgress} from "@/lib/finance/bulkClassify";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {selectCopy} from "@/lib/selectCopy";

type Kind = "category" | "transfer"

interface Failure {
  id: string
  message: string
}

/**
 * "Classificar em massa": the same "classificar como" and value for every
 * selected line. It is the one-by-one classification, repeated from the screen:
 * a few at a time, each line with its own Idempotency-Key (kept across a retry),
 * a progress bar, a stop, and the lines that failed kept and listed instead of
 * dropped. A category has a direction (income or expense), so entradas and saídas
 * together can only go to a transfer.
 */
export function UnclassifiedBulk({lines, selected, onSelected, account, accounts, onFinished}: {
  lines: UnclassifiedLine[]
  selected: ReadonlySet<string>
  onSelected: (update: (cur: ReadonlySet<string>) => ReadonlySet<string>) => void
  account: string
  accounts: Account[]
  /** A run ended: lines left the list, so focus must not fall to <body>. */
  onFinished?: () => void
}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const qc = useQueryClient();
  const [kindChoice, setKind] = useState<Kind>("category");
  const [target, setTarget] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [progress, setProgress] = useState<BulkProgress | null>(null);
  const [failures, setFailures] = useState<Failure[]>([]);
  const stop = useRef<AbortController | null>(null);
  const intents = useRef(new Map<string, Intent>());

  const chosen = lines.filter(l => selected.has(l.tx_id));
  const direction = directionOf(chosen);
  const running = progress !== null;
  // Entradas and saídas together have no one category: the kind falls to a transfer.
  const kind: Kind = direction === "mixed" ? "transfer" : kindChoice;
  const categories = accounts.filter(a => a.class === (direction === "credit" ? "income" : "expense") && !a.archived && !a.system);
  const transfers = accounts.filter(a => a.class === "asset" && !a.archived && !a.system && a.id !== account && a.source !== "byok");
  const options = (kind === "category" ? categories : transfers).map(a => ({value: a.id, label: accountName(a)}));
  const targetLabel = options.find(o => o.value === target)?.label ?? "";
  const body = (): BulkBody => (kind === "category" ? {category_id: target} : {transfer_account_id: target});

  const allSelected = lines.length > 0 && lines.every(l => selected.has(l.tx_id));
  const someSelected = chosen.length > 0 && !allSelected;
  const toggleAll = () => onSelected(() => (allSelected ? new Set() : new Set(lines.map(l => l.tx_id))));

  async function run(ids: string[]) {
    const ctl = new AbortController();
    stop.current = ctl;
    setFailures([]);
    const res = await runBulk(ids, body(), {
      signal: ctl.signal,
      intents: intents.current,
      onProgress: setProgress,
      send: async (id, b, key) => {
        await classify(ctx, id, b, key);
        // The line leaves the list at once and the selection with it.
        qc.setQueryData<{data: UnclassifiedLine[]}>(connectionKeys.unclassified(ctx, account), cur => cur && {data: cur.data.filter(x => x.tx_id !== id)});
        onSelected(cur => new Set([...cur].filter(x => x !== id)));
      },
    });
    stop.current = null;
    setProgress(null);
    onFinished?.();
    void qc.invalidateQueries({queryKey: financeKeys.all(ctx.mode, ctx.space)});
    const failed = res.failed.map(f => ({id: f.id, message: messageFor(f.error)}));
    setFailures(failed);
    if (failed.length === 0 && !res.cancelled) {
      toast.success(t("connections.unclassified.bulk.finished", {count: res.done.length}));
      setTarget("");
    } else if (res.cancelled) {
      toast(t("connections.unclassified.bulk.stopped", {done: res.done.length, total: ids.length}));
    }
  }

  const panel = useRef<HTMLElement>(null);
  // The bar rides along only while the controls are out of sight (no observer, no hiding).
  const [panelSeen, setPanelSeen] = useState(false);
  useEffect(() => {
    const el = panel.current;
    if (!el || typeof IntersectionObserver === "undefined") return undefined;
    const io = new IntersectionObserver(([e]) => setPanelSeen(e.isIntersecting));
    io.observe(el);
    return () => io.disconnect();
  }, []);
  const jump = () => {
    panel.current?.scrollIntoView?.({block: "start", behavior: "smooth"});
    panel.current?.querySelector<HTMLElement>("#bulk-target, #bulk-kind")?.focus({preventScroll: true});
  };

  return (
    <>
    <section ref={panel} aria-label={t("connections.unclassified.bulk.title")} className="scroll-mt-20 space-y-3 rounded-lg border border-border bg-muted/30 p-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <label className="flex items-center gap-2 text-sm text-foreground touch:min-h-11">
          <Checkbox checked={allSelected} indeterminate={someSelected} disabled={running} onCheckedChange={toggleAll}/>
          <span>{t("connections.unclassified.bulk.selectAll")}</span>
        </label>
        <p className="text-sm text-muted-foreground" aria-live="polite">{t("connections.unclassified.bulk.selected", {count: chosen.length})}</p>
        {chosen.length > 0 && !running && (
          <Button variant="ghost" size="sm" onClick={() => onSelected(() => new Set())}>{t("connections.unclassified.bulk.clear")}</Button>
        )}
      </div>

      {chosen.length > 0 && !running && (
        <div className="grid gap-2 lg:grid-cols-[12rem_minmax(0,16rem)_auto] lg:items-end">
          <Field label={t("connections.unclassified.bulk.kind")} htmlFor="bulk-kind">
            <Select {...selectCopy()} id="bulk-kind" value={kind} onValueChange={v => { setKind(v as Kind); setTarget(""); }}
              options={[...(direction === "mixed" ? [] : [{value: "category", label: t("connections.unclassified.category")}]),
                {value: "transfer", label: t("connections.unclassified.transfer")}]}/>
          </Field>
          <Field label={kind === "category" ? t("connections.unclassified.bulk.category") : t("connections.unclassified.bulk.account")} htmlFor="bulk-target">
            <Select {...selectCopy()} id="bulk-target" value={target} onValueChange={setTarget} options={options}/>
          </Field>
          <Button variant="brand" size="sm" disabled={!target} onClick={() => setConfirming(true)} className="max-lg:w-full">
            {t("connections.unclassified.bulk.apply", {count: chosen.length})}
          </Button>
          {direction === "mixed" && <p className="text-sm text-muted-foreground lg:col-span-3">{t("connections.unclassified.bulk.mixed")}</p>}
        </div>
      )}

      {progress && (
        <div className="space-y-2">
          <div role="progressbar" aria-label={t("connections.unclassified.bulk.title")} aria-valuemin={0} aria-valuemax={progress.total}
            aria-valuenow={progress.done + progress.failed} className="h-2 overflow-hidden rounded-full bg-border">
            <div className="h-full bg-brand transition-[width]" style={{width: `${progress.total ? ((progress.done + progress.failed) / progress.total) * 100 : 0}%`}}/>
          </div>
          <div className="flex items-center justify-between gap-3">
            <p className="text-sm text-foreground" aria-live="polite">
              {t("connections.unclassified.bulk.progress", {done: progress.done + progress.failed, total: progress.total})}
            </p>
            <Button variant="outline" size="sm" onClick={() => stop.current?.abort()}>{t("connections.unclassified.bulk.stop")}</Button>
          </div>
        </div>
      )}

      {failures.length > 0 && !running && (
        <div role="alert" className="space-y-2 text-sm">
          <p className="font-medium text-danger">{t("connections.unclassified.bulk.failed", {count: failures.length})}</p>
          <ul className="space-y-1 text-muted-foreground">
            {failures.map(f => <li key={f.id}>{`${lines.find(l => l.tx_id === f.id)?.memo || f.id}: ${f.message}`}</li>)}
          </ul>
          <Button variant="outline" size="sm" disabled={!target} onClick={() => void run(failures.map(f => f.id))}>
            {t("connections.unclassified.bulk.retry")}
          </Button>
        </div>
      )}

      <Modal open={confirming} onClose={() => setConfirming(false)} title={t("connections.unclassified.bulk.confirmTitle", {count: chosen.length})}
        description={t("connections.unclassified.bulk.confirmBody", {target: targetLabel})}
        submitLabel={t("connections.unclassified.bulk.apply", {count: chosen.length})} cancelLabel={t("connections.unclassified.bulk.cancel")}
        onSubmit={() => { setConfirming(false); void run(chosen.map(l => l.tx_id)); }}/>
    </section>
    {/* Choosing is done at the bottom of a long list: the count and the way back to the controls ride along. */}
    {chosen.length > 0 && !running && !panelSeen && (
      <div className="z-20 -mx-1 flex items-center justify-between gap-3 rounded-lg border border-border bg-background px-3 py-1.5 shadow-sm [@media(min-height:500px)]:sticky [@media(min-height:500px)]:top-(--app-shell-bar)">
        <span aria-hidden className="text-sm text-foreground">{t("connections.unclassified.bulk.selected", {count: chosen.length})}</span>
        <Button variant="brand" size="sm" onClick={jump}>{t("connections.unclassified.bulk.apply", {count: chosen.length})}…</Button>
      </div>
    )}
    </>
  );
}
