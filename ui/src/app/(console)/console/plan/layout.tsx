import type {Metadata} from "next";

export const metadata: Metadata = {title: "Plano · Console"};

export default function Layout({children}: LayoutProps<"/console/plan">) {
  return children;
}
