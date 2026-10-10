import type {Metadata} from "next"

export const metadata: Metadata = {title: "A pagar e a receber"}

export default function Layout({children}: LayoutProps<"/finance/bills">) {
  return children
}
