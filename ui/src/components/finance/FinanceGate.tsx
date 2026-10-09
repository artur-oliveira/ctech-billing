"use client"

import type {ReactNode} from "react"

import type {Verb} from "@/lib/api/financeTypes"
import {useFinanceSpaces} from "@/lib/finance/useFinanceSpaces"

/**
 * Renders its children only when the current space holds the verb.
 *
 * Absent, not disabled: a control the role can never use is noise, and the
 * server answers 403 regardless. This is presentation, never authorization.
 */
export function FinanceGate({verb, children}: {verb: Verb; children: ReactNode}) {
  const {can} = useFinanceSpaces()
  return can(verb) ? <>{children}</> : null
}
