import type {Metadata} from "next";

export const metadata: Metadata = {title: "Webhooks · Console"};

export default function Layout({children}: LayoutProps<"/console/webhooks">) {
  return children;
}
