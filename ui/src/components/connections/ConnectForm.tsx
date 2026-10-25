"use client";

import {Button, Field, FileDrop, Input, Select, Checkbox} from "@aoctech/ui";
import {useState, type FormEvent} from "react";
import {useTranslation} from "react-i18next";

import {codeMessage} from "@/lib/errors";
import {messageFor, problemCode} from "@/lib/api/client";
import type {ConnectForm as ConnectFormValues} from "@/lib/api/connectionsTypes";
import {selectCopy} from "@/lib/selectCopy";

const MAX_PEM = 16 * 1024;
const MONTHS = [3, 6, 9, 12];
const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

type Text = "client_id" | "client_secret" | "account" | "pix_key" | "notify_email"
type Errors = Partial<Record<Text | "cert" | "key" | "authorized", string>>

const EMPTY = {client_id: "", client_secret: "", account: "", pix_key: "", notify_email: ""};

/**
 * Step 5 of the onboarding: the integration's credentials, sent once to be
 * tested and sealed by CTech's outbound Lambda (BYOK spec § 3.2, § 3.3).
 *
 * The values live in this component's state only: never in a query cache, the
 * URL or storage, and the state is emptied after a successful connect. The
 * secret is a password field the browser does not offer to save.
 */
export function ConnectForm({onSubmit, defaultEmail}: {onSubmit: (v: ConnectFormValues) => Promise<void>; defaultEmail?: string}) {
  const {t} = useTranslation();
  const [v, setV] = useState({...EMPTY, notify_email: defaultEmail ?? ""});
  const [cert, setCert] = useState<File | null>(null);
  const [key, setKey] = useState<File | null>(null);
  const [months, setMonths] = useState(3);
  const [authorized, setAuthorized] = useState(false);
  const [errors, setErrors] = useState<Errors>({});
  const [server, setServer] = useState<string>();
  const [pending, setPending] = useState(false);

  const set = (k: Text) => (e: {target: {value: string}}) => {
    setV(cur => ({...cur, [k]: e.target.value}));
    setErrors(cur => ({...cur, [k]: undefined}));
  };

  function validate(): Errors {
    const e: Errors = {};
    const required = t("connections.form.required");
    if (!v.client_id.trim()) e.client_id = required;
    if (!v.client_secret) e.client_secret = required;
    if (!v.account.trim()) e.account = required;
    else if (!/^\d+$/.test(v.account.trim())) e.account = t("connections.form.invalidAccount");
    if (!EMAIL.test(v.notify_email.trim())) e.notify_email = t("connections.form.invalidEmail");
    if (!cert) e.cert = required;
    if (!key) e.key = required;
    if (!authorized) e.authorized = required;
    return e;
  }

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setServer(undefined);
    const e = validate();
    setErrors(e);
    if (Object.values(e).some(Boolean) || !cert || !key) return;
    setPending(true);
    try {
      await onSubmit({
        client_id: v.client_id.trim(), client_secret: v.client_secret, account: v.account.trim().replace(/^0+/, ""),
        pix_key: v.pix_key.trim(), notify_email: v.notify_email.trim(), backfill_months: months, authorized, cert, key,
      });
      setV(EMPTY);
      setCert(null);
      setKey(null);
      setAuthorized(false);
    } catch (err) {
      const code = problemCode(err);
      setServer(code?.startsWith("byok_") ? codeMessage(code) : messageFor(err));
    } finally {
      setPending(false);
    }
  }

  const fileLabels = {
    chooseLabel: t("connections.form.choose"), replaceLabel: t("connections.form.replace"), removeLabel: t("connections.form.remove"),
    tooLargeLabel: t("connections.form.tooLarge"), wrongTypeLabel: t("connections.form.wrongType"),
  };

  return (
    <form noValidate onSubmit={submit} className="grid gap-5">
      <div className="grid gap-4 md:grid-cols-2">
        <Field label={t("connections.form.clientId")} htmlFor="connect-client-id" error={errors.client_id}>
          <Input id="connect-client-id" value={v.client_id} onChange={set("client_id")} autoComplete="off" spellCheck={false}
            aria-invalid={errors.client_id ? true : undefined}/>
        </Field>
        <Field label={t("connections.form.clientSecret")} htmlFor="connect-client-secret" error={errors.client_secret}>
          <Input id="connect-client-secret" type="password" value={v.client_secret} onChange={set("client_secret")} autoComplete="off"
            spellCheck={false} aria-invalid={errors.client_secret ? true : undefined}/>
        </Field>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <FileDrop id="connect-cert" label={t("connections.form.cert")} accept=".crt,.pem,.cer" maxBytes={MAX_PEM} file={cert}
          onFile={f => { setCert(f); setErrors(cur => ({...cur, cert: undefined})); }} error={errors.cert} {...fileLabels}/>
        <FileDrop id="connect-key" label={t("connections.form.key")} accept=".key,.pem" maxBytes={MAX_PEM} file={key}
          onFile={f => { setKey(f); setErrors(cur => ({...cur, key: undefined})); }} error={errors.key} {...fileLabels}/>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <Field label={t("connections.form.account")} htmlFor="connect-account" hint={t("connections.form.accountHint")} error={errors.account}>
          <Input id="connect-account" inputMode="numeric" value={v.account} onChange={set("account")} autoComplete="off"
            aria-invalid={errors.account ? true : undefined}/>
        </Field>
        <Field label={t("connections.form.backfill")} htmlFor="connect-backfill">
          <Select {...selectCopy()} id="connect-backfill" value={String(months)} onValueChange={m => setMonths(Number(m))}
            options={MONTHS.map(m => ({value: String(m), label: t("connections.form.backfillMonths", {count: m})}))}/>
        </Field>
        <Field label={t("connections.form.pixKey")} htmlFor="connect-pix-key" hint={t("connections.form.pixKeyHint")}>
          <Input id="connect-pix-key" value={v.pix_key} onChange={set("pix_key")} autoComplete="off" spellCheck={false}/>
        </Field>
        <Field label={t("connections.form.notifyEmail")} htmlFor="connect-notify-email" hint={t("connections.form.notifyEmailHint")} error={errors.notify_email}>
          <Input id="connect-notify-email" type="email" value={v.notify_email} onChange={set("notify_email")} autoComplete="email"
            aria-invalid={errors.notify_email ? true : undefined}/>
        </Field>
      </div>

      <div className="grid gap-1.5">
        <label className="flex items-start gap-2.5 text-sm text-foreground">
          <Checkbox checked={authorized} onCheckedChange={c => { setAuthorized(Boolean(c)); setErrors(cur => ({...cur, authorized: undefined})); }}
            className="mt-0.5 shrink-0" aria-invalid={errors.authorized ? true : undefined}/>
          <span className="text-pretty">{t("connections.form.authorize")}</span>
        </label>
        {errors.authorized && <p role="alert" className="pl-7 text-sm text-danger">{errors.authorized}</p>}
      </div>

      {server && <p role="alert" className="text-sm text-danger">{server}</p>}

      <div>
        <Button type="submit" variant="brand" disabled={pending} aria-busy={pending || undefined} className="max-sm:w-full">
          {pending ? t("connections.form.testing") : t("connections.form.submit")}
        </Button>
      </div>
    </form>
  );
}
