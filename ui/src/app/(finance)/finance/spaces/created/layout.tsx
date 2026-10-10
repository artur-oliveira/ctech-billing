import type {Metadata} from "next"

export const metadata: Metadata = {title: "Finanças"}

export default function Layout({children}: LayoutProps<"/finance/spaces/created">) {
  return children
}
