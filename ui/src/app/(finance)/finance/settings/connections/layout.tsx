import type {Metadata} from "next";

export const metadata: Metadata = {title: "Conexões · Finanças"};

export default function Layout({children}: LayoutProps<"/finance/settings/connections">) {
  return children;
}
