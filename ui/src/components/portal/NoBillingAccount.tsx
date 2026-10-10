"use client"

import {Button, EmptyState} from "@aoctech/ui"
import {Receipt} from "lucide-react"
import {useTranslation} from "react-i18next"

import {usePortalSpaces} from "@/components/portal/PortalSpaceSwitch"
import {usePortalSpace} from "@/lib/portal/space"

const ACCOUNTS = process.env.NEXT_PUBLIC_CTECH_CLIENT_URL || "https://accounts.aoctech.app"

/**
 * Signed in, with nothing bought yet.
 *
 * The API answers 403 and has to: there is no customer behind the session, so
 * no route below it can serve one. But 403 is the mechanism, not the meaning —
 * this person did nothing wrong and nothing failed. Rendered as an error, they
 * are greeted by a red block quoting a sentence written for a log ("nenhuma
 * conta de cobrança para este usuário") and are left to guess whether the
 * portal is broken or they are.
 *
 * So it is an empty state, in the same shape as the one P1 shows a customer
 * who owes nothing — the two states are neighbours, and a person who cancels
 * their last subscription should not cross a visual cliff into a failure
 * screen.
 *
 * It replaces the portal rather than sitting inside it, like the terms gate:
 * every screen below would show three copies of this, one per failed query.
 */
export function NoBillingAccount() {
  const {t} = useTranslation()
  const space = usePortalSpace()
  const {hasOrganizations} = usePortalSpaces()
  // Pointed at the switch above when the person's own account is the empty
  // one and an organization's may not be.
  const description = hasOrganizations && space.kind === "personal"
    ? `${t("portal.noAccount.description")} ${t("portal.space.pickOrganization")}`
    : t("portal.noAccount.description")
  return (
    <EmptyState
      icon={<Receipt/>}
      title={t("portal.noAccount.title")}
      description={description}
      action={
        <Button
          variant="outline"
          render={<a href={ACCOUNTS} rel="noreferrer"/>}
        >
          {t("portal.noAccount.action")}
        </Button>
      }
    />
  )
}
