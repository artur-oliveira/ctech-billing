import {describe, expect, it} from "vitest"

import en from "@/locales/en"
import ptBR from "@/locales/pt-BR"

function keys(value: unknown, prefix = ""): string[] {
  if (typeof value === "string") return [prefix]
  return Object.entries(value as object).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k))
}

function strings(value: unknown): string[] {
  if (typeof value === "string") return [value]
  return Object.values(value as object).flatMap(strings)
}

/** Same {{placeholders}} in both languages, or a translation drops a value. */
const vars = (s: string) => [...s.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map(m => m[1]).sort().join(",")

describe("locale catalogs", () => {
  it("have the same keys in pt-BR and en", () => {
    expect(keys(en).sort()).toEqual(keys(ptBR).sort())
  })

  it("use the same placeholders in both languages", () => {
    const flat = (o: unknown) => Object.fromEntries(keys(o).map(k => [k, k.split(".").reduce<unknown>((a, p) => (a as Record<string, unknown>)[p], o) as string]))
    const a = flat(ptBR), b = flat(en)
    for (const k of Object.keys(a)) expect([k, vars(b[k])]).toEqual([k, vars(a[k])])
  })

  it("contain no em dash", () => {
    expect([...strings(ptBR), ...strings(en)].filter(s => s.includes("—"))).toEqual([])
  })
})
