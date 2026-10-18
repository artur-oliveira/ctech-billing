"use client";

import {Input} from "@aoctech/ui";
import type {ComponentProps} from "react";

import {maskMoney, moneyPlaceholder} from "@/lib/money";

type MoneyInputProps = Omit<ComponentProps<typeof Input>, "value" | "onChange" | "inputMode" | "type"> & {
  /** What is typed, in the mask's notation ("1.234,56"); parse it with `parseMoney`. */
  value: string
  /** Receives the masked text, never the raw keystrokes. */
  onValueChange: (masked: string) => void
  /** Opening balances and corrections may be negative. */
  signed?: boolean
}

/**
 * The money control of every finance form: a decimal keypad on a phone
 * (`inputMode`, not `type=number`, which drops the mask and the locale's comma),
 * the mask applied as you type, the locale's placeholder. Wire errors with
 * `{...fe.props(field, id)}`; the caller still parses with `parseMoney`.
 */
export function MoneyInput({value, onValueChange, signed, placeholder, ...rest}: MoneyInputProps) {
  return (
    <Input
      autoComplete="off"
      enterKeyHint="done"
      placeholder={placeholder ?? moneyPlaceholder()}
      {...rest}
      inputMode="decimal"
      value={value}
      onChange={e => onValueChange(maskMoney(e.target.value, signed ? {signed: true} : undefined))}
    />
  );
}
