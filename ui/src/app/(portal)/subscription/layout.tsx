import type {Metadata} from "next"

// One static file serves every subscription, so the plan's own name is not
// knowable at build time. The page replaces this once the data lands
// (useDocumentTitle), exactly as the invoice route does.
export const metadata: Metadata = {title: "Assinatura"}

export default function Layout({children}: LayoutProps<"/subscription">) {
  return children
}
