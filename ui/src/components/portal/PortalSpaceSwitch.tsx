"use client"

import {Select} from "@aoctech/ui"
import {useQuery, useQueryClient} from "@tanstack/react-query"
import {useRouter} from "next/navigation"
import {useEffect} from "react"
import {useTranslation} from "react-i18next"
import {toast} from "sonner"

import {listPortalSpaces, portalKeys} from "@/lib/api/portal"
import {parseSpace, PERSONAL, spaceHeader} from "@/lib/console/space"
import {useAuth} from "@/lib/auth/AuthContext"
import {setPortalSpace, usePortalSpace} from "@/lib/portal/space"
import {selectCopy} from "@/lib/selectCopy"

/**
 * The portal's list of whose bills to show: Pessoal, then the organizations the
 * person owns or administers, by name. Shared by the switch and by the empty
 * state, which reads it to know whether there is anywhere else to look.
 */
export function usePortalSpaces() {
  const {authenticated} = useAuth()
  const query = useQuery({queryKey: portalKeys.spaces, queryFn: listPortalSpaces, enabled: authenticated})
  const spaces = query.data?.spaces ?? []
  return {
    spaces,
    loaded: query.isSuccess,
    unavailable: query.data?.organizations_unavailable ?? false,
    hasOrganizations: spaces.some(s => s.selector !== "personal"),
  }
}

/**
 * Whose bills — Pessoal or an organization — in the console's form: a select
 * beside the avatar that names the current answer, no label in front of it.
 * On a phone it takes the header's second row, so a long organization name
 * still reads. Shown only when there is somewhere other than Pessoal to go:
 * most people never manage an organization, and they never see it.
 *
 * The value is a request the server re-authorizes on every call (ADR 0025); the
 * switch only makes the current answer visible. Every portal query is keyed by
 * the space, so the other organization's invoices never show under the new
 * name; switching lands on Início, since an invoice open in one organization
 * does not exist in the next.
 */
export function PortalSpaceSwitch() {
  const {t} = useTranslation()
  const router = useRouter()
  const queryClient = useQueryClient()
  const space = usePortalSpace()
  const {spaces, loaded, unavailable, hasOrganizations} = usePortalSpaces()
  const current = spaceHeader(space)

  // A remembered organization the server no longer lists — demoted, removed,
  // or a value from another account on this browser. Only when the list is a
  // real answer: "unavailable" says nothing about membership.
  useEffect(() => {
    if (!loaded || unavailable || space.kind !== "organization") return
    if (!spaces.some(s => s.selector === current)) {
      setPortalSpace(PERSONAL)
      toast.info(t("portal.space.lost"), {id: "portal-space-lost"})
    }
  }, [loaded, unavailable, space, spaces, current, t])

  if (!hasOrganizations) return null

  const choose = async (value: string) => {
    if (value === current) return
    // The old space's requests are not wanted any more; its cache is a
    // separate key and simply stops being read (portalKeys, PortalSpaceScope).
    await queryClient.cancelQueries({queryKey: ["portal"]})
    setPortalSpace(parseSpace(value))
    router.push("/dashboard")
  }

  return (
    <Select {...selectCopy()}
      aria-label={t("portal.space.label")}
      className="order-3 w-full min-w-0 sm:order-2 sm:ml-auto sm:w-auto sm:min-w-36 sm:max-w-56"
      value={current}
      onValueChange={v => void choose(v)}
      options={spaces.map(s => ({
        value: s.selector,
        label: s.selector === "personal" ? t("portal.space.personal") : s.display_name,
      }))}
    />
  )
}
