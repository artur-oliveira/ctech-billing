"use client"

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
 * A native select on purpose: a standard affordance, keyboard and screen-reader
 * complete, at the console's compact height.
 */
export function SpaceSwitch() {
  const space = useSpace()
  const {spaces, unavailable, spaceOf} = useFinanceSpaces()

  return (
    <div className="flex items-center gap-2">
      <label className="sr-only" htmlFor="finance-space">Espaço</label>
      <select
        id="finance-space"
        value={spaceHeader(space)}
        onChange={e => setSpace(parseSpace(e.target.value))}
        className="h-8 max-w-48 truncate rounded-lg border border-border bg-background px-2 text-xs text-foreground focus-visible:outline-2 focus-visible:outline-ring"
      >
        {spaces.length === 0 && <option value="personal">Pessoal</option>}
        {spaces.map(e => {
          const value = spaceHeader(spaceOf(e))
          return <option key={value} value={value}>{e.label}</option>
        })}
      </select>
      {unavailable && <span className="text-xs text-muted-foreground">Organizações indisponíveis agora</span>}
    </div>
  )
}
