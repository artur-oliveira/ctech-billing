"use client"

import {Plus} from "lucide-react"
import {useTranslation} from "react-i18next"

import {Select} from "@/components/ui/Select"
import {spaceHeader, parseSpace, setSpace} from "@/lib/console/space"
import {useSpace} from "@/lib/console/useSpace"
import * as handoff from "@/lib/finance/spaceHandoff"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"

/**
 * Which space the finance section is looking at.
 *
 * A closed list from the server — Pessoal first, then the person's personal
 * workspaces, then their organizations — and never a field anybody can type into: the value is a
 * request the server re-authorizes on every call (ADR 0025), so the control's
 * job is to make the current answer visible, sitting next to the mode switch
 * so "Pessoal · Teste" is read as one thing.
 *
 * The shared styled Select (components/ui/Select), which shows the space's
 * name and never its "org:{id}" value once chosen. "Novo espaço" is the list's
 * last entry, behind a divider: creating a space is a way of answering "which
 * space", so it lives where that question is asked, not as a second button
 * beside it. Choosing it starts the ctech-account handoff and leaves the
 * current space as it was.
 */
export function SpaceSwitch() {
  const {t} = useTranslation()
  const space = useSpace()
  const {spaces, current, unavailable, spaceOf, labelOf} = useFinanceSpaces()
  // Only the owner of a personal workspace manages its people, and only in
  // ctech-account: billing keeps no member list (ADR 0027).
  const manage = current?.manage_people && space.kind === "organization" ? space.organizationId : null

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 max-sm:flex-1">
      <Select
        aria-label={t("finance.space.label")}
        className="min-w-36 max-w-56 max-sm:max-w-none max-sm:flex-1"
        value={spaceHeader(space)}
        onValueChange={v => setSpace(parseSpace(v))}
        options={spaces.length === 0
          ? [{value: "personal", label: t("finance.space.personal")}]
          : spaces.map(e => ({value: spaceHeader(spaceOf(e)), label: labelOf(e)}))}
        actions={[{label: t("finance.space.new"), icon: <Plus/>, onSelect: () => handoff.startCreateSpace()}]}
      />
      {manage && (
        <a
          href={handoff.managePeopleURL(manage, window.location.origin)}
          className="inline-flex items-center text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline touch:min-h-11"
        >
          {t("finance.space.managePeople")}
        </a>
      )}
      {unavailable && <span className="text-xs text-muted-foreground">{t("finance.space.orgsUnavailable")}</span>}
    </div>
  )
}
