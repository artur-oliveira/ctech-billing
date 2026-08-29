import type {Metadata} from "next"

export const metadata: Metadata = {title: "Catálogo · Console"}

export default function Layout({children}: LayoutProps<"/console/catalog">) {
  return children
}
