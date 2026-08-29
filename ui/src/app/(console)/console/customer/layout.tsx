import type {Metadata} from "next"

export const metadata: Metadata = {title: "Cliente · Console"}

export default function Layout({children}: LayoutProps<"/console/customer">) {
  return children
}
