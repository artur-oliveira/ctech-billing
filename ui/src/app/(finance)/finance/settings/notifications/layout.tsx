import type {Metadata} from "next";

export const metadata: Metadata = {title: "Notificações · Finanças"};

export default function Layout({children}: LayoutProps<"/finance/settings/notifications">) {
  return children;
}
