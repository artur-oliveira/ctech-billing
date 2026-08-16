import type {Metadata} from "next"

// The build-time title. The invoice's number is not knowable here — this route
// is one static file serving every invoice — so the page replaces it with
// "Fatura nº 1042" once the data lands. See useDocumentTitle.
export const metadata: Metadata = {title: "Fatura"}

export default function Layout({children}: LayoutProps<"/invoice">) {
  return children
}
