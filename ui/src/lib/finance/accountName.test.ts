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

  it("names the categories billing's own invoices post under", async () => {
    await changeAppLanguage("en")
    expect(accountName({name: "Assinaturas", system_key: "subscriptions_revenue"})).toBe("Subscriptions")
    expect(accountName({name: "Assinaturas CTech", system_key: "ctech_subscriptions"})).toBe("CTech subscriptions")
    await changeAppLanguage("pt-BR")
    expect(accountName({name: "Assinaturas CTech", system_key: "ctech_subscriptions"})).toBe("Assinaturas CTech")
  })
})
