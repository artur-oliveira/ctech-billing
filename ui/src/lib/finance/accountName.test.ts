import {describe, expect, it} from "vitest"

import {changeAppLanguage} from "@/lib/i18n"

import {accountName} from "./accountName"

describe("accountName", () => {
  it("translates an untouched default and leaves renamed or own accounts alone", async () => {
    await changeAppLanguage("en")
    expect(accountName({name: "Salário", system_key: "salary"})).toBe("Salary")
    expect(accountName({name: "Meu salário", system_key: "salary"})).toBe("Meu salário")
    expect(accountName({name: "Salário"})).toBe("Salário")
    await changeAppLanguage("pt-BR")
    expect(accountName({name: "Salário", system_key: "salary"})).toBe("Salário")
  })
})
