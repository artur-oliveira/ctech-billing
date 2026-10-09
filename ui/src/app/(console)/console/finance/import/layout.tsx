import type {Metadata} from "next"

export const metadata: Metadata = {title: "Importar"}

export default function Layout({children}: LayoutProps<"/console/finance/import">) {
  return children
}
