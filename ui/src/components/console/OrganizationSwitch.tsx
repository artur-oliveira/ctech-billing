"use client"

import {Select} from "@aoctech/ui"
import {useQueryClient} from "@tanstack/react-query"
import {useTranslation} from "react-i18next"

import type {MerchantOrganization} from "@/lib/api/consoleTypes"
import {setMerchantOrg} from "@/lib/console/merchant"
import {selectCopy} from "@/lib/selectCopy"

/**
 * Which organization the billing half acts for (ADR 0028): a closed list from
 * the session, the organizations where the person is owner or admin. The value
 * is a request the server re-authorizes on every call. Switching removes the
 * previous organization's queries, so none of its rows can show for a frame
 * under the new name.
 */
export function OrganizationSwitch({organizations, selected}: {organizations: MerchantOrganization[]; selected: MerchantOrganization}) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  return (
    <Select {...selectCopy()}
      aria-label={t("console.organization.label")}
      className="w-full min-w-0"
      value={selected.organization_id}
      onValueChange={v => {
        if (v === selected.organization_id) return
        setMerchantOrg(v)
        void queryClient.removeQueries({queryKey: ["console", selected.organization_id]})
      }}
      options={organizations.map(o => ({value: o.organization_id, label: o.display_name}))}
    />
  )
}
