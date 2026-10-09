import {Button, EmptyState} from "@aoctech/ui"
import {Building2} from "lucide-react"
import Link from "next/link"
import {useTranslation} from "react-i18next"

/**
 * Signed in, and not an operator.
 *
 * Everybody who signs in is a customer; only some also hold an organization,
 * and that is provisioned rather than self-served (assessment D4). So this is
 * not an error state and must not read like one — it is a person who followed a
 * link to a part of the product that is not theirs, and the useful thing to do
 * is send them back to their own bills.
 */
export function NoOrganization() {
  const {t} = useTranslation()
  return (
    <EmptyState
      icon={<Building2/>}
      title={t("console.noOrg.title")}
      description={t("console.noOrg.description")}
      action={
        <div className="flex flex-wrap gap-2">
          <Button render={<Link href="/console/finance"/>}>{t("console.noOrg.finance")}</Button>
          <Button variant="outline" render={<Link href="/dashboard"/>}>{t("console.noOrg.bills")}</Button>
        </div>
      }
    />
  )
}
