import {act, renderHook} from "@testing-library/react"
import {describe, expect, it} from "vitest"

import {useFieldErrors} from "@/lib/useFieldErrors"

const problem = (errors: {field: string; code: string; params?: Record<string, number>}[], status = 422) =>
  ({response: {status, data: {status, errors}}})

describe("useFieldErrors", () => {
  it("puts each error on its own field and clears one when edited", () => {
    const {result} = renderHook(() => useFieldErrors(["amount", "due_date"]))
    let validation = false
    act(() => {
      validation = result.current.set(problem([
        {field: "amount", code: "required"},
        {field: "due_date", code: "invalid_format", params: {}},
      ]))
    })
    expect(validation).toBe(true)
    expect(result.current.of("amount")).toBe("Obrigatório.")
    expect(result.current.of("due_date")).toMatch(/Formato inválido/)
    expect(result.current.props("amount", "x")).toEqual({"aria-invalid": true, "aria-describedby": "x-error"})
    expect(result.current.general).toBeUndefined()
    act(() => result.current.clear("amount"))
    expect(result.current.of("amount")).toBeUndefined()
    expect(result.current.of("due_date")).toBeDefined()
    expect(result.current.props("amount", "x")).toEqual({})
  })

  it("maps indexed paths to their control and sends unknown paths to general", () => {
    const {result} = renderHook(() => useFieldErrors(["items[1]", "items"]))
    act(() => {
      result.current.set(problem([
        {field: "items[1].quantity", code: "required"},
        {field: "items[0].quantity", code: "required"},
        {field: "mystery", code: "not_found"},
      ]))
    })
    expect(result.current.of("items[1]")).toBe("Obrigatório.")
    expect(result.current.of("items")).toBe("Obrigatório.")
    expect(result.current.general).toBe("Não encontrado.")
  })

  it("keeps the single message for failures that are not validation", () => {
    const {result} = renderHook(() => useFieldErrors(["amount"]))
    let validation = true
    act(() => { validation = result.current.set(problem([], 409)) })
    expect(validation).toBe(false)
    expect(result.current.general).toBeTruthy()
    expect(result.current.of("amount")).toBeUndefined()
  })

  it("can leave other failures to a toast", () => {
    const {result} = renderHook(() => useFieldErrors(["amount"], {keepOthers: false}))
    act(() => { result.current.set(problem([], 500)) })
    expect(result.current.general).toBeUndefined()
  })

  it("reset drops everything", () => {
    const {result} = renderHook(() => useFieldErrors(["amount"]))
    act(() => { result.current.set(problem([{field: "amount", code: "required"}, {field: "x", code: "required"}])) })
    act(() => result.current.reset())
    expect(result.current.of("amount")).toBeUndefined()
    expect(result.current.general).toBeUndefined()
  })
})
