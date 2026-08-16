import {Button} from "@aoctech/ui"
import type {Metadata} from "next"
import Link from "next/link"

import {StatusScreen} from "@/components/StatusScreen"

export const metadata: Metadata = {title: "Página não encontrada"}

/**
 * 404. Almost always a mistyped or expired link rather than a bug, so it says
 * that plainly and offers the one destination that is certainly there. No
 * search box: this portal has four screens.
 */
export default function NotFound() {
  return (
    <StatusScreen
      title="Esta página não existe"
      description="O endereço pode estar incompleto, ou o link que você seguiu pode ter expirado. Nada foi cobrado e nenhuma fatura foi alterada."
      action={<Button render={<Link href="/" />}>Ir para o início</Button>}
    />
  )
}
