import type {Metadata} from "next"

export const metadata: Metadata = {title: "A pagar e a receber"}

export default function Layout({children}: LayoutProps<"/console/finance/bills">) {
  return children
}
