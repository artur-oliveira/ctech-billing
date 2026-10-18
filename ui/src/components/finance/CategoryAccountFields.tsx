"use client";

import {Field, Select} from "@aoctech/ui";
import type {ReactNode} from "react";

import type {Account, Direction} from "@/lib/api/financeTypes";
import {accountName} from "@/lib/finance/accountName";
import {selectCopy} from "@/lib/selectCopy";

const live = (a: Account) => !a.archived && !a.system;

/** The categories a bill or recurrence of this direction can use (expense for payable, income for receivable). */
export const categoriesFor = (accounts: Account[], direction: Direction): Account[] =>
  accounts.filter(a => a.class === (direction === "payable" ? "expense" : "income") && live(a));

/** The asset accounts money moves through. */
export const assetsOf = (accounts: Account[]): Account[] => accounts.filter(a => a.class === "asset" && live(a));

export const accountOptions = (accounts: Account[]) => accounts.map(a => ({value: a.id, label: accountName(a)}));

interface PickerProps {
  id: string
  label: string
  value: string
  onValueChange: (value: string) => void
  /** The Field's error and the control's aria (`fe.props(...)`). */
  error?: string
  controlProps?: Record<string, unknown>
  required?: boolean
  /** Said under the select when there is nothing to pick ("Sem categoria. Crie em Contas."). */
  emptyHint?: string
  className?: string
}

function Picker({options, id, label, value, onValueChange, error, controlProps, required, emptyHint, className}: PickerProps & {options: {value: string; label: string; icon?: ReactNode}[]}) {
  return (
    <Field label={label} htmlFor={id} required={required} error={error} hint={options.length === 0 ? emptyHint : undefined} className={className}>
      <Select {...selectCopy()} id={id} value={value} {...controlProps} onValueChange={onValueChange} options={options}/>
    </Field>
  );
}

/** The category select of a bill or recurrence form: only the categories of its direction. */
export function CategoryField({accounts, direction, ...p}: PickerProps & {accounts: Account[]; direction: Direction}) {
  return <Picker {...p} options={accountOptions(categoriesFor(accounts, direction))}/>;
}

/** The "paid from / received in" select: asset accounts, plus `extraOptions` (a recurrence also offers cards). */
export function AssetAccountField({accounts, extraOptions = [], ...p}: PickerProps & {accounts: Account[]; extraOptions?: {value: string; label: string; icon?: ReactNode}[]}) {
  return <Picker {...p} options={[...accountOptions(assetsOf(accounts)), ...extraOptions]}/>;
}
