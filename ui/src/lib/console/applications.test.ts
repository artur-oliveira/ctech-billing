import {describe, expect, it} from "vitest"

import {applyFieldErrors, isHttpsURL, MONTHLY_VOLUMES, rejectReasonErrors, toApplyRequest} from "@/lib/console/applications"

const ok = {
  company_id: "cmp_1", description: "Assinaturas de pão para empresas do bairro", website: "",
  expected_monthly_volume: "" as const, contact_email: "c@padaria.example",
}
const codes = (errs: {field: string; code: string}[]) => Object.fromEntries(errs.map(e => [e.field, e.code]))

describe("applyFieldErrors", () => {
  it("accepts a complete form with the optional fields empty", () => {
    expect(applyFieldErrors(ok)).toEqual([])
  })
  it("names every problem at once, with the API's codes", () => {
    expect(codes(applyFieldErrors({company_id: "", description: "curta", website: "http://x.example", expected_monthly_volume: "", contact_email: "nope"})))
      .toEqual({company_id: "required", description: "too_short", website: "invalid_url", contact_email: "invalid_email"})
  })
  it("counts characters, not bytes, and trims before counting", () => {
    expect(applyFieldErrors({...ok, description: "  " + "ç".repeat(20) + "   "})).toEqual([])
    expect(codes(applyFieldErrors({...ok, description: "x".repeat(1001)}))).toEqual({description: "too_long"})
  })
})

describe("toApplyRequest", () => {
  it("trims and leaves out empty optional fields", () => {
    expect(toApplyRequest({...ok, description: "  " + ok.description + " ", website: " "})).toEqual({
      company_id: "cmp_1", description: ok.description, contact_email: "c@padaria.example",
    })
  })
})

describe("isHttpsURL", () => {
  it.each([
    ["https://padaria.example", true], ["https://padaria.example/loja?x=1", true],
    ["http://padaria.example", false], ["padaria.example", false], ["https://", false], ["https://u:p@x.example", false],
  ])("%s is %s", (v, want) => expect(isHttpsURL(v)).toBe(want))
})

describe("rejectReasonErrors", () => {
  it("wants 10 to 500 characters", () => {
    expect(codes(rejectReasonErrors("curto"))).toEqual({reason: "too_short"})
    expect(codes(rejectReasonErrors(""))).toEqual({reason: "required"})
    expect(rejectReasonErrors("O site não abre.")).toEqual([])
  })
})

it("lists the four volumes in order", () => {
  expect(MONTHLY_VOLUMES).toEqual(["up_to_10k", "10k_to_50k", "50k_to_200k", "over_200k"])
})
