"use client";

import {type ReactNode, useRef} from "react";

import {useElementWidth} from "@/lib/hooks/useElementWidth";
import {NARROW, useMediaQuery} from "@/lib/hooks/useMediaQuery";

interface ContainerSwitchProps {
  /** What the wide layout needs, in px: below it the container is "narrow". */
  min: number
  className?: string
  children: (narrow: boolean) => ReactNode
}

/**
 * Chooses between two layouts (a table and a list) by the width of the box it
 * is in, not of the window: a sidebar takes 240px at tablet widths, so the
 * window says "wide" while the content has less than a phone. Where the width
 * cannot be measured (no ResizeObserver, before the first measure) the viewport
 * media query stands in, so server and test renders stay the phone/desktop split.
 */
export function ContainerSwitch({min, className, children}: ContainerSwitchProps) {
  const ref = useRef<HTMLDivElement>(null);
  const width = useElementWidth(ref);
  const media = useMediaQuery(NARROW);
  return <div ref={ref} className={className}>{children(width > 0 ? width < min : media)}</div>;
}
