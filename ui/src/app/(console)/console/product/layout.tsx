import type {Metadata} from "next"

export const metadata: Metadata = {title: "Produto · Console"}

export default function Layout({children}: LayoutProps<"/console/product">) {
  return children
}
