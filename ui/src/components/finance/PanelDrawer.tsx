"use client";

import {Drawer} from "@aoctech/ui";
import type {ComponentProps} from "react";
import {useTranslation} from "react-i18next";

type DrawerProps = ComponentProps<typeof Drawer>;

/**
 * The shell every "new X" / "edit X" panel of Finanças sits in: a Drawer that is
 * open only when asked AND allowed (a role without the verb never sees it, even
 * if a request for it arrived first), and that mounts its form only while open,
 * so each opening starts from a clean form.
 *
 * The drawer's own X is the one way to dismiss it: forms inside carry no "Close"
 * button next to Save. Its name comes from @aoctech/ui's catalog, which defaults
 * to pt-BR, so the UI language is passed down.
 */
export function PanelDrawer({open, allowed = true, children, ...drawer}: Omit<DrawerProps, "children"> & {allowed?: boolean; children: React.ReactNode}) {
  const {i18n} = useTranslation();
  const shown = open && allowed;
  return <Drawer open={shown} locale={i18n.language} {...drawer}>{shown ? children : null}</Drawer>;
}
