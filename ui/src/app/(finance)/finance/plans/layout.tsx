import type {Metadata} from "next"

export const metadata: Metadata = {title: "Plano · Finanças"}

export default function Layout({children}: LayoutProps<"/finance/plans">) {
  return children
}
