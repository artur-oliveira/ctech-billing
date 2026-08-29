import type {Metadata} from "next"

export const metadata: Metadata = {title: "Clientes · Console"}

export default function Layout({children}: LayoutProps<"/console/customers">) {
  return children
}
