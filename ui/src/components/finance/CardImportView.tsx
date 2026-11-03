"use client";

import {Button, Field, FileDrop, Input, Select, Skeleton} from "@aoctech/ui";
import {useQuery} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {FormError, FormErrorText} from "@/components/finance/FormError";
import {InlineConfirm} from "@/components/finance/InlineConfirm";
import {LedgerRow} from "@/components/finance/LedgerRow";
import {wholeSpace} from "@/components/finance/TransferPanel";
import {ErrorBlock} from "@/components/portal/ErrorBlock";
import {Amount} from "@/components/ui/Amount";
import {Section} from "@/components/ui/Section";
import {
  confirmAllCardLines, confirmCardLines, createCardLine, type FinanceCtx, fileToBase64, financeKeys, getBankCategories, getCardImport, getCardStatement,
  ignoreLine, putBankCategories, reopenStatement, uploadCardImport,
} from "@/lib/api/finance";
import type {Account, Card, CardImportLine, CardSuggestion} from "@/lib/api/financeTypes";
import {accountName} from "@/lib/finance/accountName";
import {shiftMonth} from "@/lib/finance/statementTimeline";
import {monthShort} from "@/lib/finance/today";
import {useFinanceMutation} from "@/lib/finance/useFinanceMutation";
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces";
import {money, shortDate} from "@/lib/format";
import {selectCopy} from "@/lib/selectCopy";

const MAX_BYTES = 1024 * 1024;
const MONTH = /^\d{4}-(0[1-9]|1[0-2])$/;

/** The statement month a bank file name carries ("fatura-inter-2026-11.csv"), if any. */
const monthOfName = (name: string) => {
  const m = /(\d{4}-(?:0[1-9]|1[0-2]))/.exec(name);
  return m ? m[1] : undefined;
};

/**
 * Import a bank's card statement into the card's open statement: pick the month and
 * the file, then review. Each line the person already keyed by hand is offered as a
 * pair to confirm (or all at once); the rest becomes a purchase or a credit, or is
 * ignored. Importing the same file again adds nothing twice.
 */
export function CardImportView({card, accounts, importId: initial, onClose}: {card: Card; accounts: Account[]; importId?: string; onClose: () => void}) {
  const {t} = useTranslation();
  const [month, setMonth] = useState(card.open_month);
  const [file, setFile] = useState<File | null>(null);
  const [importId, setImportId] = useState(initial ?? "");
  const [note, setNote] = useState<string | null>(null);
  const [reopening, setReopening] = useState(false);
  const valid = MONTH.test(month);
  const target = !valid ? "invalid" : month === card.open_month ? "open" : month === shiftMonth(card.open_month, -1) ? "closed" : "other";

  const upload = useFinanceMutation(
    async (c: FinanceCtx, f: File, key) => uploadCardImport(c, card.id, {statement_month: month, bank: "inter", content: await fileToBase64(f)}, key),
    wholeSpace,
    result => {
      if (result.id) {
        setImportId(result.id);
        setNote(t("finance.cardImport.result", {added: result.lines, duplicates: result.duplicates, rejected: result.rejected_count}));
      } else setNote(t("finance.cardImport.nothingNew"));
    },
  );
  const reopen = useFinanceMutation((c: FinanceCtx, _: void, key) => reopenStatement(c, card.id, month, key), wholeSpace, () => setReopening(false));

  return (
    <div className="space-y-4">
      <form
        aria-label={t("finance.cardImport.title")}
        className="space-y-3"
        onSubmit={e => {
          e.preventDefault();
          if (!file || target !== "open" || upload.isPending) return;
          setNote(null);
          upload.mutate(file);
        }}
      >
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t("finance.cardImport.month")} htmlFor="ci-month">
            <Input id="ci-month" type="month" value={month} onChange={e => { setMonth(e.target.value); setNote(null); setReopening(false); }}/>
          </Field>
          <FileDrop id="ci-file" label={t("finance.cardImport.file")} hint={t("finance.cardImport.fileHint")} accept=".csv,.txt" maxBytes={MAX_BYTES} file={file}
            onFile={f => {
              setFile(f); setNote(null); upload.reset(); upload.newIntent();
              const named = f ? monthOfName(f.name) : undefined;
              if (named) setMonth(named);
            }}
            chooseLabel={t("finance.import.choose")} replaceLabel={t("finance.import.replace")} removeLabel={t("finance.import.remove")}
            tooLargeLabel={t("finance.import.tooLarge")} wrongTypeLabel={t("finance.import.wrongType")}/>
        </div>
        {target === "other" && <p role="status" className="text-sm text-muted-foreground">{t("finance.cardImport.onlyOpen", {open: monthShort(card.open_month)})}</p>}
        {target === "closed" && !reopening && (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-muted-foreground">{t("finance.cardImport.closedNote", {month: monthShort(month)})}</span>
            <Button type="button" variant="outline" size="sm" onClick={() => setReopening(true)}>{t("finance.cardImport.reopen")}</Button>
          </div>
        )}
        {target === "closed" && reopening && (
          <InlineConfirm tone="brand" text={t("finance.cardImport.reopenConfirm", {month: monthShort(month)})} cancelLabel={t("finance.cards.back")} confirmLabel={t("finance.cards.confirm")}
            pending={reopen.isPending} error={reopen.error ? <FormErrorText error={reopen.error}/> : null} onCancel={() => setReopening(false)} onConfirm={() => reopen.mutate()}/>
        )}
        <div className="flex flex-wrap items-center gap-2">
          {target === "open" && <Button type="submit" variant="brand" size="sm" disabled={!file || upload.isPending}>{upload.isPending ? t("finance.import.sending") : t("finance.cardImport.submit")}</Button>}
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>{t("finance.cardImport.close")}</Button>
        </div>
        {note && <p role="status" className="text-sm text-foreground">{note}</p>}
        <FormError error={upload.error} className=""/>
      </form>
      {importId && <Review key={importId} card={card} accounts={accounts} importId={importId}/>}
    </div>
  );
}

function Review({card, accounts, importId}: {card: Card; accounts: Account[]; importId: string}) {
  const {t} = useTranslation();
  const ctx = useFinanceCtx();
  const q = useQuery({queryKey: financeKeys.cardImport(ctx.mode, ctx.space, importId), queryFn: () => getCardImport(ctx, importId)});
  const cats = useQuery({queryKey: financeKeys.bankCategories(ctx.mode, ctx.space, card.id), queryFn: () => getBankCategories(ctx, card.id)});
  const month = q.data?.import.statement_month;
  const statement = useQuery({
    queryKey: financeKeys.cardStatement(ctx.mode, ctx.space, card.id, month ?? ""),
    queryFn: () => getCardStatement(ctx, card.id, month!), enabled: !!month,
  });
  const invalidate = (c: FinanceCtx) => [financeKeys.cardImport(c.mode, c.space, importId), financeKeys.all(c.mode, c.space)];
  const confirm = useFinanceMutation((c: FinanceCtx, s: CardSuggestion, key) =>
    confirmCardLines(c, importId, {lines: s.lines, purchase_id: s.item!.purchase_id, tag: s.item!.tag}, key), invalidate);
  const confirmAll = useFinanceMutation((c: FinanceCtx, _: void, key) => confirmAllCardLines(c, importId, key), invalidate);
  const create = useFinanceMutation((c: FinanceCtx, l: CardImportLine, key) =>
    createCardLine(c, importId, l.n, {category_id: cats.data?.categories[l.bank_category ?? ""]}, key), invalidate);
  const ignore = useFinanceMutation((c: FinanceCtx, l: CardImportLine, key) => ignoreLine(c, importId, l.n, key), invalidate);
  const map = useFinanceMutation((c: FinanceCtx, v: {bank: string; id: string}, key) =>
    putBankCategories(c, card.id, {...cats.data?.categories, [v.bank]: v.id}, key), c => [financeKeys.bankCategories(c.mode, c.space, card.id)]);

  if (q.isLoading || cats.isLoading) return <div className="space-y-2" aria-busy><Skeleton className="h-5 w-48"/><Skeleton className="h-4 w-full"/></div>;
  if (q.error || !q.data) return <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>;
  const {lines, suggestions} = q.data;
  const byN = new Map(lines.map(l => [l.n, l]));
  const matches = suggestions.filter(s => s.kind !== "ignore" && s.item);
  const paired = new Set(matches.flatMap(s => s.lines));
  const rest = lines.filter(l => l.status === "pending" && !paired.has(l.n));
  const count = (status: string) => lines.filter(l => l.status === status).length;
  const expense = accounts.filter(a => a.class === "expense" && !a.system && !a.archived);
  const mapped = cats.data?.categories ?? {};
  const fileTotal = lines.filter(l => l.status !== "ignored").reduce((sum, l) => sum + l.amount, 0);
  const stTotal = statement.data?.total;

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        {t("finance.cardImport.counts", {pending: count("pending"), linked: count("linked"), created: count("created"), ignored: count("ignored")})}
      </p>
      {stTotal !== undefined && (
        <p role="status" aria-label={t("finance.cardImport.totalsLabel")} className="text-sm text-foreground">
          {t("finance.cardImport.totals", {file: money(fileTotal), statement: money(stTotal)})}
          {fileTotal !== stTotal && <> {t("finance.cardImport.totalsDiffer")}</>}
        </p>
      )}
      {matches.length > 0 && (
        <Section title={t("finance.cardImport.pairs")} className="space-y-2">
          <Button type="button" variant="brand" size="sm" disabled={confirmAll.isPending} onClick={() => confirmAll.mutate()}>
            {t("finance.cardImport.confirmAll", {count: matches.length})}
          </Button>
          <FormError error={confirmAll.error} className=""/>
          <ul className="divide-y divide-border border-y border-border">
            {matches.map(s => {
              const first = byN.get(s.lines[0])!;
              return (
                <LedgerRow key={`${s.lines.join("-")}`} title={s.item!.description}
                  meta={<>{s.lines.map(n => byN.get(n)?.description).join(" + ")} · {shortDate(first.date)}{s.kind === "group" ? ` · ${t("finance.cardImport.group", {count: s.lines.length})}` : ""}</>}
                  amount={<Amount>{money(s.item!.amount)}</Amount>}
                  actions={<Button type="button" size="sm" variant="outline" disabled={confirm.isPending} onClick={() => confirm.mutate(s)}>{t("finance.cardImport.confirm")}</Button>}/>
              );
            })}
          </ul>
          <FormError error={confirm.error} className=""/>
        </Section>
      )}
      <Section title={t("finance.cardImport.unpaired")} className="space-y-2">
        {rest.length === 0 ? <p className="text-sm text-muted-foreground">{t("finance.cardImport.empty")}</p> : (
          <ul className="divide-y divide-border border-y border-border">
            {rest.map(l => {
              const bank = l.bank_category ?? "";
              const ready = !!mapped[bank];
              return (
                <LedgerRow key={l.n} title={l.description}
                  meta={<>{shortDate(l.date)}{l.of ? ` · ${t("finance.cardImport.installment", {k: l.k, of: l.of})}` : ""}{bank ? ` · ${bank}` : ""}</>}
                  amount={<Amount>{money(l.amount)}</Amount>}
                  actions={<>
                    {!l.payment && !ready && (
                      <Select {...selectCopy()} aria-label={t("finance.cardImport.categoryFor", {bank})} value="" placeholder={t("finance.cardImport.chooseCategory")}
                        onValueChange={id => map.mutate({bank, id})} options={expense.map(a => ({value: a.id, label: accountName(a)}))}/>
                    )}
                    {!l.payment && (
                      <Button type="button" size="sm" variant="outline" disabled={!ready || create.isPending} onClick={() => create.mutate(l)}>
                        {l.amount < 0 ? t("finance.cardImport.createCredit") : t("finance.cardImport.createPurchase")}
                      </Button>
                    )}
                    <Button type="button" size="sm" variant="ghost" disabled={ignore.isPending} onClick={() => ignore.mutate(l)}>{t("finance.cardImport.ignore")}</Button>
                  </>}/>
              );
            })}
          </ul>
        )}
        <FormError error={create.error ?? ignore.error ?? map.error} className=""/>
      </Section>
    </div>
  );
}
