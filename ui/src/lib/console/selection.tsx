"use client";

import {createContext, type ReactNode, useContext} from "react";

import type {MerchantOrganization} from "@/lib/api/consoleTypes";
import type {ConsoleScope} from "@/lib/console/merchant";

/**
 * The selected organization, whether or not it has a tenant in this mode.
 * The invitation, the apply form and the status band read it; the billing
 * screens keep reading useConsoleScope, which exists only with a tenant.
 */
export type MerchantSelection = {scope: ConsoleScope; organization: MerchantOrganization}

const SelectionContext = createContext<MerchantSelection | null>(null);

export function MerchantSelectionProvider({value, children}: {value: MerchantSelection; children: ReactNode}) {
  return <SelectionContext.Provider value={value}>{children}</SelectionContext.Provider>;
}

export function useMerchantSelection(): MerchantSelection {
  const v = useContext(SelectionContext);
  if (!v) throw new Error("useMerchantSelection: render inside the console shell, after an organization is selected");
  return v;
}
