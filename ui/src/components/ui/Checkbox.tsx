"use client";

import {Checkbox as UiCheckbox} from "@aoctech/ui";
import type {ComponentProps} from "react";

/**
 * @aoctech/ui's Checkbox, with the one thing it gets wrong: it draws the check
 * glyph for the indeterminate state too, and "some of them" must read as a dash.
 * The glyph is hidden and a bar drawn in its place. Upstream candidate
 * (docs/specs/2026-10-17-ui-upstream-proposals.md); drop this wrapper when
 * ctech-ui's Checkbox does it.
 */
const DASH = "data-[indeterminate]:[&_svg]:hidden data-[indeterminate]:after:block data-[indeterminate]:after:h-0.5 data-[indeterminate]:after:w-2.5 data-[indeterminate]:after:rounded-full data-[indeterminate]:after:bg-white";

export function Checkbox({className = "", ...props}: ComponentProps<typeof UiCheckbox>) {
  return <UiCheckbox className={`${DASH} ${className}`.trim()} {...props}/>;
}
