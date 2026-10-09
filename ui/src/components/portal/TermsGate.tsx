"use client"

import {Button, Checkbox} from "@aoctech/ui"
import {useMutation, useQueryClient} from "@tanstack/react-query"
import {useState} from "react"
import {Trans, useTranslation} from "react-i18next"
import {toast} from "sonner"

import {messageFor} from "@/lib/api/client"
import {acceptTerms, portalKeys} from "@/lib/api/portal"
import type {Session} from "@/lib/api/types"
import {BILLING_TERMS_URL, PRIVACY_POLICY_URL} from "@/lib/legal"

/**
 * The billing terms addendum, asked once and asked again when it changes.
 *
 * It exists because signing in through CTech SSO never presents a checkbox for
 * terms specific to a product the person had not opened yet — the same reason
 * ctech-dfe and ctech-wallet each carry one. The document itself lives in
 * ctech-account's legal centre; this screen links to it and records the answer.
 *
 * **It replaces the portal content rather than floating over it.** A dismissible
 * dialogue over a readable page is a request; this is a condition of using the
 * product, and rendering it as an overlay someone can scroll behind would be
 * asking for consent while already delivering the thing.
 *
 * What it deliberately does **not** gate: the public checkout. Somebody paying a
 * bill from an emailed link has no session to record an answer against, and
 * putting a consent wall in front of a payment is refusing money over a document
 * they do not need to have read in order to owe it. That page carries the same
 * links in its footer instead.
 */
export function TermsGate() {
  const {t} = useTranslation()
  const [checked, setChecked] = useState(false)
  const queryClient = useQueryClient()

  const accept = useMutation({
    mutationFn: acceptTerms,
    // Written straight into the cache. The gate is dismissed by this value
    // changing, so refetching first would leave the modal on screen for a round
    // trip after the click that resolved it.
    onSuccess: (session: Session) => queryClient.setQueryData(portalKeys.session, session),
    onError: error => toast.error(messageFor(error)),
  })

  return (
    <section aria-labelledby="termos" className="space-y-6">
      <header className="space-y-2">
        <h1 id="termos" className="text-2xl font-semibold tracking-[-0.02em] text-foreground">
          {t("portal.terms.title")}
        </h1>
        <p className="text-pretty text-sm text-muted-foreground">
          {t("portal.terms.description")}
        </p>
      </header>

      <div className="rounded-xl border border-border bg-surface p-5">
        <label className="flex items-start gap-3 text-sm text-foreground">
          <Checkbox
            checked={checked}
            onCheckedChange={value => setChecked(Boolean(value))}
            aria-describedby="termos-links"
          />
          <span id="termos-links" className="text-pretty">
            <Trans
              i18nKey="portal.terms.agree"
              components={{
                terms: (
                  <a
                    href={BILLING_TERMS_URL}
                    target="_blank"
                    rel="noreferrer"
                    className="font-medium text-brand-600 underline underline-offset-4"
                  />
                ),
                privacy: (
                  <a
                    href={PRIVACY_POLICY_URL}
                    target="_blank"
                    rel="noreferrer"
                    className="font-medium text-brand-600 underline underline-offset-4"
                  />
                ),
              }}
            />
          </span>
        </label>
      </div>

      <Button
        block
        disabled={!checked || accept.isPending}
        onClick={() => accept.mutate()}
      >
        {accept.isPending ? t("portal.terms.confirming") : t("portal.terms.continue")}
      </Button>
    </section>
  )
}
