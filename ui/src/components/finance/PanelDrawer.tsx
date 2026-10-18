"use client";

import {Drawer} from "@aoctech/ui";
import type {ComponentProps} from "react";

type DrawerProps = ComponentProps<typeof Drawer>;

/**
 * The shell every "new X" / "edit X" panel of Finanças sits in: a Drawer that is
 * open only when asked AND allowed (a role without the verb never sees it, even
 * if a request for it arrived first), and that mounts its form only while open,
 * so each opening starts from a clean form.
 */
export function PanelDrawer({open, allowed = true, children, ...drawer}: Omit<DrawerProps, "children"> & {allowed?: boolean; children: React.ReactNode}) {
  const shown = open && allowed;
  return <Drawer open={shown} {...drawer}>{shown ? children : null}</Drawer>;
}
