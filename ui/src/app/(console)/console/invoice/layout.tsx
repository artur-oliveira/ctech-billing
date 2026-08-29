import type {Metadata} from "next"

// One static file serves every invoice, so the number is not knowable here.
// The page replaces it once the data lands.
export const metadata: Metadata = {title: "Fatura · Console"}

export default function Layout({children}: LayoutProps<"/console/invoice">) {
  return children
}
