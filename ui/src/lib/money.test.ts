import {describe, expect, it} from "vitest"

import {formatMoneyInput, limitMoneyDecimals, parseMoney, parseSignedMoney} from "@/lib/money"

describe("parseMoney", () => {
  it.each([
    ["1.234,56", 123456],
    ["1234,56", 123456],
    ["0,05", 5],
    ["0,5", 50],
    ["10", 1000],
    ["10,", 1000],
    ["R$ 10", 1000],
    ["R$ 1.000,00", 100000],
    ["  42,10  ", 4210],
    ["1.000", 100000], // a thousands group, not a decimal: pt-BR
  ])("reads %s as %i centavos", (text, cents) => {
    expect(parseMoney(text)).toBe(cents)
  })

  it.each([
    "", " ", "abc", "1,234,56", "1.2.3", "-5", "1e3", "0", "0,00", "1234.5", "10,999",
    "92233720368547758,07",
  ])("refuses %j", text => {
    expect(parseMoney(text)).toBeNull()
  })

  it("never goes through a float", () => {
    // 0.07 * 100 is 7.000000000000001 in floating point; the answer must be exactly 7.
    expect(parseMoney("0,07")).toBe(7)
    expect(parseMoney("1.005,05")).toBe(100505)
  })

  it("prints thousands like a person types them", () => {
    expect(formatMoneyInput(180000)).toBe("1.800,00")
    expect(formatMoneyInput(123456789)).toBe("1.234.567,89")
    expect(formatMoneyInput(5)).toBe("0,05")
  })

  it("round-trips what it prints", () => {
    for (const cents of [1, 5, 99, 100, 123456, 100000000]) {
      expect(parseMoney(formatMoneyInput(cents))).toBe(cents)
    }
  })
})

describe("parseSignedMoney", () => {
  it("reads an overdrawn balance with either minus sign", () => {
    expect(parseSignedMoney("-1.500,00")).toBe(-150000)
    expect(parseSignedMoney("−20")).toBe(-2000)
    expect(parseSignedMoney("1.500,00")).toBe(150000)
  })
  it("refuses what parseMoney refuses, and a lone sign", () => {
    expect(parseSignedMoney("-")).toBeNull()
    expect(parseSignedMoney("--5")).toBeNull()
    expect(parseSignedMoney("-0")).toBeNull()
  })
})

describe("limitMoneyDecimals", () => {
  it.each([
    ["10,999", "10,99"],
    ["1.234,5", "1.234,5"],
    ["1.234,56", "1.234,56"],
    ["-20,123", "-20,12"],
    ["100", "100"],
    ["", ""],
  ])("keeps %j as %j while typing", (typed, kept) => {
    expect(limitMoneyDecimals(typed)).toBe(kept)
  })
})
