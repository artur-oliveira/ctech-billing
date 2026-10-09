"use client"

import {useTranslation} from "react-i18next"

import {Select} from "@/components/ui/Select"
import {spaceHeader, parseSpace, setSpace} from "@/lib/console/space"
import {useSpace} from "@/lib/console/useSpace"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"

/**
 * Which space the finance section is looking at.
 *
 * A closed list from the server — personal first, then the person's
 * organizations — and never a field anybody can type into: the value is a
 * request the server re-authorizes on every call (ADR 0025), so the control's
 * job is to make the current answer visible, sitting next to the mode switch
 * so "Pessoal · Teste" is read as one thing.
 *
 * The shared styled Select (components/ui/Select), which shows the space's
 * name and never its "org:{id}" value once chosen.
 */
export function SpaceSwitch() {
  const {t} = useTranslation()
  const space = useSpace()
  const {spaces, unavailable, spaceOf} = useFinanceSpaces()

  return (
    <div className="flex items-center gap-2">
      <Select
        aria-label={t("finance.space.label")}
        className="max-w-52"
        value={spaceHeader(space)}
        onValueChange={v => setSpace(parseSpace(v))}
        options={spaces.length === 0
          ? [{value: "personal", label: t("finance.space.personal")}]
          : spaces.map(e => ({value: spaceHeader(spaceOf(e)), label: e.label}))}
      />
      {unavailable && <span className="text-xs text-muted-foreground">{t("finance.space.orgsUnavailable")}</span>}
    </div>
  )
}
