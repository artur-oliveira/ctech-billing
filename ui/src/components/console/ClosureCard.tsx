"use client";

import {Alert, Button, Field, Input} from "@aoctech/ui";
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {messageFor, problemCode} from "@/lib/api/client";
import {
  CLOSURE_PHRASE,
  closureKeys,
  getClosure,
  getClosureBlockers,
  requestClosure,
  type ClosureBlocker,
} from "@/lib/api/closure";
import {useConsoleScope} from "@/lib/console/scope";
import {instantDate} from "@/lib/format";

/**
 * Closing the billing account (docs/specs/2026-10-17-merchant-tenant-deletion.md).
 *
 * A quiet, separate danger zone at the bottom of Settings. It renders nothing for
 * anybody who is not an owner: the API answers 403 `owner_required` and a card
 * that offers what the server will refuse is a button that lies.
 */
export function ClosureCard() {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  const queryClient = useQueryClient();
  const [phrase, setPhrase] = useState("");

  const status = useQuery({queryKey: closureKeys.status(scope), queryFn: () => getClosure(scope), retry: false});
  const blockers = useQuery({
    queryKey: closureKeys.blockers(scope),
    queryFn: () => getClosureBlockers(scope),
    enabled: status.isSuccess && status.data === null,
    retry: false,
  });
  const request = useMutation({
    mutationFn: () => requestClosure(scope, phrase),
    onSuccess: () => queryClient.invalidateQueries({queryKey: closureKeys.status(scope)}),
  });

  // Not an owner (403), no billing account (404 on blockers) or still loading: nothing to offer.
  if (status.isPending || status.isError) return null;

  const open = status.data;
  const date = (iso?: string) => (iso ? instantDate(iso) : "");
  const blockerText = (b: ClosureBlocker) => t(`closure.blockers.${b.code}`, {count: b.count, defaultValue: b.code});
  const refused = request.isError ? problemCode(request.error) : undefined;
  const requestBlockers: ClosureBlocker[] =
    refused === "closure_blocked"
      ? ((request.error as {response?: {data?: {blockers?: ClosureBlocker[]}}}).response?.data?.blockers ?? [])
      : [];
  const listed = requestBlockers.length > 0 ? requestBlockers : (blockers.data ?? []);

  return (
    <section className="space-y-4 rounded-lg border border-border p-4">
      <h2 className="text-base font-semibold tracking-[-0.01em] text-foreground">{t("closure.title")}</h2>
      <p className="text-sm text-muted-foreground">{t("closure.intro")}</p>

      {open ? (
        <Alert tone="warning" title={t("closure.title")}>
          {open.state === "blocked"
            ? t("closure.state.blocked", {blockers: (open.blockers ?? []).join(", ")})
            : t(`closure.state.${open.state}`, {date: date(open.grace_until ?? open.confirm_by)})}
        </Alert>
      ) : (
        <>
          <ol className="list-decimal space-y-1 pl-5 text-sm">
            {(t("closure.steps", {returnObjects: true}) as string[]).map((s) => <li key={s}>{s}</li>)}
          </ol>
          {listed.length > 0 && (
            <Alert tone="danger" title={t("closure.blockedTitle")}>
              <ul className="list-disc pl-5">{listed.map((b) => <li key={b.code}>{blockerText(b)}</li>)}</ul>
            </Alert>
          )}
          {request.isError && refused !== "closure_blocked" && (
            <Alert tone="danger" title={t("closure.title")}>{messageFor(request.error)}</Alert>
          )}
          {request.isSuccess ? (
            <Alert tone="success" title={t("closure.title")}>
              {t("closure.checkEmail", {date: date(request.data.confirm_by)})}
            </Alert>
          ) : (
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                request.mutate();
              }}
            >
              <Field label={t("closure.phraseLabel", {phrase: CLOSURE_PHRASE})} htmlFor="closure-phrase">
                <Input id="closure-phrase" value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off"/>
              </Field>
              <Button type="submit" variant="danger" disabled={phrase !== CLOSURE_PHRASE || request.isPending || listed.length > 0}>
                {t("closure.submit")}
              </Button>
            </form>
          )}
        </>
      )}
    </section>
  );
}
