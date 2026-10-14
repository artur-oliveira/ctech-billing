import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen} from "@testing-library/react"
import {afterEach, describe, expect, it, vi} from "vitest"

import ConsoleCatalogPage from "@/app/(console)/console/catalog/page"
import * as consoleApi from "@/lib/api/console"
import {ConsoleScopeProvider} from "@/lib/console/scope"
import {clearPendingCreate, requestCreate} from "@/lib/createRequest"

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const

function wrap() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><ConsoleScopeProvider value={SCOPE}><ConsoleCatalogPage/></ConsoleScopeProvider></QueryClientProvider>)
}

afterEach(() => {
  vi.restoreAllMocks()
  clearPendingCreate()
})

describe("/console/catalog", () => {
  it("opens the new-product form when the search asks for it", async () => {
    vi.spyOn(consoleApi, "listConsoleProducts").mockResolvedValue({data: [], has_more: false} as never)
    requestCreate("product")
    wrap()
    expect(await screen.findByRole("dialog")).toBeInTheDocument()
  })

  it("stays closed without a request", async () => {
    vi.spyOn(consoleApi, "listConsoleProducts").mockResolvedValue({data: [], has_more: false} as never)
    wrap()
    await screen.findAllByRole("button", {name: "Novo produto"})
    expect(screen.queryByRole("dialog")).toBeNull()
  })
})
