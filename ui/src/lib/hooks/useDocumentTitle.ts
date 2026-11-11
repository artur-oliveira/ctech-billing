"use client";

import {useEffect} from "react";

const SUFFIX = "CTech Billing";

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
    if (title === null) return undefined;
    const full = `${title} · ${SUFFIX}`;
    const apply = () => { if (document.title !== full) document.title = full; };
    apply();
    // Next writes the route's static metadata title after this effect ran (on
    // hydration and again on each client navigation), which put the pt-BR title
    // back. Re-assert ours whenever the <title> is rewritten, until the title
    // changes or the screen unmounts.
    // The <head> is observed, not the <title> node: Next replaces the node on
    // navigation, which a watcher attached to the old one would never see.
    const observer = new MutationObserver(apply);
    observer.observe(document.head, {childList: true, characterData: true, subtree: true});
    return () => observer.disconnect();
  }, [title]);
}
