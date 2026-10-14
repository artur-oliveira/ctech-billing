"use client";

import {createContext, Fragment, type ReactNode, useContext} from "react";

import type {ConsoleScope} from "@/lib/console/merchant";

const ScopeContext = createContext<ConsoleScope | null>(null);

/** Provided by the console shell once an organization with a tenant in this
 *  mode is selected; nothing below it renders before that. The subtree is
 *  keyed by the scope: a switch (here or in another tab) remounts it, so an
 *  open form never carries one organization's draft into another. */
export function ConsoleScopeProvider({value, children}: {value: ConsoleScope; children: ReactNode}) {
  return (
    <ScopeContext.Provider value={value}>
      <Fragment key={`${value.org}:${value.mode}`}>{children}</Fragment>
    </ScopeContext.Provider>
  );
}

/** The (mode, organization) every billing-half call and query key is made with. */
export function useConsoleScope(): ConsoleScope {
  const scope = useContext(ScopeContext);
  if (!scope) throw new Error("useConsoleScope: render inside the console shell, after an organization is selected");
  return scope;
}
