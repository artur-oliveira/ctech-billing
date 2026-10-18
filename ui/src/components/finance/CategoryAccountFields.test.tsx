import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {assetsOf, categoriesFor, CategoryField} from "@/components/finance/CategoryAccountFields";
import type {Account} from "@/lib/api/financeTypes";

const acct = (id: string, cls: Account["class"], over: Partial<Account> = {}): Account => ({id, name: id, class: cls, system: false, archived: false, balance: 0, ...over});
const ACCOUNTS = [acct("cc", "asset"), acct("old", "asset", {archived: true}), acct("sys", "asset", {system: true}), acct("rent", "expense"), acct("pay", "income")];

describe("category and account pickers", () => {
  it("offers the categories of the direction and only live asset accounts", () => {
    expect(categoriesFor(ACCOUNTS, "payable").map(a => a.id)).toEqual(["rent"]);
    expect(categoriesFor(ACCOUNTS, "receivable").map(a => a.id)).toEqual(["pay"]);
    expect(assetsOf(ACCOUNTS).map(a => a.id)).toEqual(["cc"]);
  });

  it("says what to do when there is nothing to pick", () => {
    render(<CategoryField id="c" label="Categoria" accounts={[]} direction="payable" value="" onValueChange={() => {}} emptyHint="Crie em Contas."/>);
    expect(screen.getByText("Crie em Contas.")).toBeInTheDocument();
  });
});
