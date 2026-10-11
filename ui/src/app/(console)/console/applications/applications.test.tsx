import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen} from "@testing-library/react"
import {afterEach, describe, expect, it, vi} from "vitest"

import ApplicationsPage from "@/app/(console)/console/applications/page"
import * as api from "@/lib/api/console"
import {ConsoleScopeProvider} from "@/lib/console/scope"

const SCOPE = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const

afterEach(() => vi.restoreAllMocks())

describe("Solicitações", () => {
  it("names the three filters as statuses, and opens on the pending queue", async () => {
    const list = vi.spyOn(api, "listApplications").mockResolvedValue({data: [], has_more: false})
    render(
      <QueryClientProvider client={new QueryClient({defaultOptions: {queries: {retry: false}}})}>
        <ConsoleScopeProvider value={SCOPE}><ApplicationsPage/></ConsoleScopeProvider>
      </QueryClientProvider>,
    )
    for (const name of ["Pendentes", "Aprovadas", "Recusadas"]) expect(screen.getByRole("button", {name})).toBeInTheDocument()
    expect(await screen.findByText("Nenhuma solicitação pendente.")).toBeInTheDocument()
    expect(list).toHaveBeenCalledWith(SCOPE, "pending", undefined)
  })
})
