import {Button, EmptyState} from "@aoctech/ui"
import {Building2} from "lucide-react"
import Link from "next/link"

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
  return (
    <EmptyState
      icon={<Building2/>}
      title="Esta conta não tem uma organização"
      description="Faturas, assinaturas e clientes são de quem emite cobranças. Suas finanças pessoais estão em Finanças, e o que você paga para a CTech, em Minhas cobranças."
      action={
        <div className="flex flex-wrap gap-2">
          <Button render={<Link href="/console/finance"/>}>Ir para Finanças</Button>
          <Button variant="outline" render={<Link href="/dashboard"/>}>Ver minhas cobranças</Button>
        </div>
      }
    />
  )
}
