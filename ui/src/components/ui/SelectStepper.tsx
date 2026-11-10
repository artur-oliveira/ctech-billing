"use client";

import {Button, Select, type SelectOption} from "@aoctech/ui";
import {ChevronLeft, ChevronRight} from "lucide-react";

import {selectCopy} from "@/lib/selectCopy";

interface SelectStepperProps {
  /** Names the select and the group for a screen reader. */
  label: string
  value: string
  onValueChange: (value: string) => void
  /** In stepping order: previous/next move one place along this list. */
  options: SelectOption[]
  prevLabel: string
  nextLabel: string
  className?: string
}

/**
 * A select with chevrons to step to the previous or next option, for an ordered
 * scale (a horizon: 3m, 6m, 1a...). The buttons stop, disabled, at either end.
 * Composes @aoctech/ui's Select and Button; a candidate for ctech-ui if a second
 * product needs it.
 */
export function SelectStepper({label, value, onValueChange, options, prevLabel, nextLabel, className = ""}: SelectStepperProps) {
  const i = options.findIndex(o => o.value === value);
  const step = (d: number) => { const o = options[i + d]; if (o) onValueChange(o.value); };
  return (
    <div role="group" aria-label={label} className={`flex items-center gap-1 ${className}`}>
      <Button type="button" variant="outline" size="icon" aria-label={prevLabel} disabled={i <= 0} onClick={() => step(-1)}><ChevronLeft aria-hidden className="size-4"/></Button>
      <Select {...selectCopy()} aria-label={label} value={value} onValueChange={onValueChange} options={options} className="min-w-0 flex-1 sm:w-28 sm:flex-none"/>
      <Button type="button" variant="outline" size="icon" aria-label={nextLabel} disabled={i < 0 || i >= options.length - 1} onClick={() => step(1)}><ChevronRight aria-hidden className="size-4"/></Button>
    </div>
  );
}
