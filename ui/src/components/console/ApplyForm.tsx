"use client";

import {Button, Field, Input, Select, Textarea} from "@aoctech/ui";
import {useMutation, useQuery} from "@tanstack/react-query";
import {useRouter} from "next/navigation";
import {useState} from "react";
import {useTranslation} from "react-i18next";

import {messageFor} from "@/lib/api/client";
import {applyForMerchant, consoleKeys, listMerchantCompanies} from "@/lib/api/console";
import type {MerchantApplication, MonthlyVolume} from "@/lib/api/consoleTypes";
import {type ApplyFormValues, applyFieldErrors, MONTHLY_VOLUMES, toApplyRequest} from "@/lib/console/applications";
import {useMerchantSelection} from "@/lib/console/selection";
import {fieldErrorMessage} from "@/lib/errors";
import limits from "@/lib/limits.json";
import {selectCopy} from "@/lib/selectCopy";
import {useFieldErrors} from "@/lib/useFieldErrors";

const FIELDS = ["company_id", "description", "website", "expected_monthly_volume", "contact_email"] as const;

/**
 * The application (spec § 4.1). Every rule is the API's; the server re-checks.
 *
 * ctech-account lists at most 200 companies by legal name. When it says it cut
 * the list, the form says so under the picker and offers to name the company by
 * its code instead; the server checks that code against the organization.
 */
export function ApplyForm({onApplied}: {onApplied: (a: MerchantApplication) => void}) {
  const {t} = useTranslation();
  const router = useRouter();
  const {scope} = useMerchantSelection();
  const [values, setValues] = useState<ApplyFormValues>({
    company_id: "", description: "", website: "", expected_monthly_volume: "", contact_email: "",
  });
  const [byCode, setByCode] = useState(false);
  const [local, setLocal] = useState<Record<string, string>>({});
  const fe = useFieldErrors([...FIELDS]);

  const companies = useQuery({
    queryKey: consoleKeys.merchantCompanies(scope),
    queryFn: () => listMerchantCompanies(scope),
    retry: false,
  });

  const apply = useMutation({
    mutationFn: () => applyForMerchant(scope, toApplyRequest({...values, company_id: values.company_id.trim()})),
    onSuccess: onApplied,
    onError: error => { fe.set(error); },
  });

  const set = <K extends keyof ApplyFormValues>(k: K, v: ApplyFormValues[K]) => {
    setValues(s => ({...s, [k]: v}));
    setLocal(s => ({...s, [k]: ""}));
    fe.clear(k);
  };
  const errorOf = (f: string) => local[f] || fe.of(f);

  const submit = () => {
    fe.reset();
    const errs = applyFieldErrors({...values, company_id: values.company_id.trim()});
    setLocal(Object.fromEntries(errs.map(e => [e.field, fieldErrorMessage(e)])));
    if (errs.length === 0) apply.mutate();
  };

  if (companies.isPending) return <div className="h-40 max-w-xl" aria-busy/>;
  if (companies.isError) {
    return (
      <div className="max-w-xl space-y-3">
        <p className="text-sm text-foreground">{messageFor(companies.error)}</p>
        <Button variant="outline" className="max-sm:w-full" onClick={() => void companies.refetch()}>{t("console.apply.retry")}</Button>
      </div>
    );
  }
  const list = companies.data.companies;
  if (list.length === 0) {
    return <p className="max-w-xl text-sm text-foreground">{t("console.apply.noCompanies")}</p>;
  }

  return (
    <form className="grid gap-5 sm:grid-cols-2" noValidate onSubmit={e => { e.preventDefault(); submit(); }}>
      {byCode ? (
        <div className="space-y-2 sm:col-span-2">
          <Field label={t("console.apply.companyId")} htmlFor="apply-company-id" hint={t("console.apply.companyIdHint")} error={errorOf("company_id")}>
            <Input id="apply-company-id" autoComplete="off" spellCheck={false} aria-invalid={!!errorOf("company_id") || undefined}
              value={values.company_id} onChange={e => set("company_id", e.target.value)}/>
          </Field>
          <Button type="button" variant="link" size="sm" className="px-0" onClick={() => { setByCode(false); set("company_id", ""); }}>
            {t("console.apply.pickFromList")}
          </Button>
        </div>
      ) : (
        <div className="space-y-2 sm:col-span-2">
          <Field label={t("console.apply.company")} htmlFor="apply-company" hint={t("console.apply.companyHint")} error={errorOf("company_id")}>
            <Select {...selectCopy()}
              id="apply-company"
              aria-label={t("console.apply.company")}
              aria-invalid={!!errorOf("company_id") || undefined}
              className="w-full"
              value={values.company_id}
              onValueChange={v => set("company_id", v)}
              options={list.map(c => ({value: c.company_id, label: `${c.legal_name} • ${c.tax_id}`}))}
            />
          </Field>
          {companies.data.truncated && (
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">{t("console.apply.truncated")}</p>
              <Button type="button" variant="link" size="sm" className="px-0" onClick={() => { setByCode(true); set("company_id", ""); }}>
                {t("console.apply.enterId")}
              </Button>
            </div>
          )}
        </div>
      )}
      <Field className="sm:col-span-2" label={t("console.apply.about")} htmlFor="apply-about" hint={t("console.apply.aboutHint")} error={errorOf("description")}>
        <Textarea id="apply-about" maxLength={limits.text.applicationDescription} rows={4}
          placeholder={t("console.apply.aboutPlaceholder")} aria-invalid={!!errorOf("description") || undefined}
          value={values.description} onChange={e => set("description", e.target.value)}/>
      </Field>
      <Field label={t("console.apply.website")} htmlFor="apply-website" error={errorOf("website")}>
        <Input id="apply-website" type="url" inputMode="url" placeholder="https://" maxLength={limits.text.website}
          aria-invalid={!!errorOf("website") || undefined}
          value={values.website} onChange={e => set("website", e.target.value)}/>
      </Field>
      <Field label={t("console.apply.volume")} htmlFor="apply-volume" error={errorOf("expected_monthly_volume")}>
        <Select {...selectCopy()}
          id="apply-volume"
          aria-label={t("console.apply.volume")}
          className="w-full"
          none
          value={values.expected_monthly_volume}
          onValueChange={v => set("expected_monthly_volume", v as MonthlyVolume | "")}
          options={MONTHLY_VOLUMES.map(v => ({value: v, label: t(`console.volume.${v}`)}))}
        />
      </Field>
      <Field className="sm:col-span-2" label={t("console.apply.contact")} htmlFor="apply-contact" hint={t("console.apply.contactHint")} error={errorOf("contact_email")}>
        <Input id="apply-contact" type="email" inputMode="email" autoComplete="email" maxLength={limits.text.email}
          aria-invalid={!!errorOf("contact_email") || undefined}
          value={values.contact_email} onChange={e => set("contact_email", e.target.value)}/>
      </Field>
      {fe.general && <p role="alert" className="text-sm text-danger sm:col-span-2">{fe.general}</p>}
      {/* Primary first on a phone (a full-width column), on the right from sm. */}
      <div className="flex flex-col gap-2 border-t border-border pt-5 sm:col-span-2 sm:flex-row-reverse sm:justify-start">
        <Button type="submit" variant="brand" disabled={apply.isPending} className="max-sm:w-full">{t("console.apply.submit")}</Button>
        <Button type="button" variant="ghost" className="max-sm:w-full" onClick={() => router.back()}>{t("console.apply.cancel")}</Button>
      </div>
    </form>
  );
}
