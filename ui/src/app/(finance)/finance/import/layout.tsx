import type {Metadata} from "next"

export const metadata: Metadata = {title: "Importar"}

export default function Layout({children}: LayoutProps<"/finance/import">) {
  return children
}
