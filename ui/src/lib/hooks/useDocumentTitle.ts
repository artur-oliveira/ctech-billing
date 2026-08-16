"use client"

import {useEffect} from "react"

const SUFFIX = "CTech Billing"

/**
 * Names the tab for something only the client knows.
 *
 * Every route here is one prerendered file, so a title that depends on the
 * data — "Fatura nº 1042" — cannot come from `metadata`. It arrives with the
 * query instead, and until then the route layout's static title stands.
 *
 * The suffix is repeated rather than imported from the metadata template:
 * `%s · CTech Billing` is a string Next expands server-side and does not
 * expose. Two copies of four words is cheaper than a config module for it.
 *
 * Passing `null` leaves the title alone, which is what a screen still loading
 * should do — replacing it with "Carregando" would make the tab flicker on
 * every navigation.
 */
export function useDocumentTitle(title: string | null) {
  useEffect(() => {
    if (title === null) return
    document.title = `${title} · ${SUFFIX}`
  }, [title])
}
