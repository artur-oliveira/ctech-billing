import "@testing-library/jest-dom/vitest"

import {screen, waitFor} from "@testing-library/react"
import {afterEach, describe, expect, it, vi} from "vitest"

import {FinanceGate} from "@/components/finance/FinanceGate"
import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as finance from "@/lib/api/finance"

afterEach(() => vi.restoreAllMocks())

describe("FinanceGate", () => {
  it("renders its children only when the current space holds the verb", async () => {
    vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({
      spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ["finance.read"], manage_people: false}],
      organizations_unavailable: false,
    })
    renderWithQuery(
      <>
        <FinanceGate verb="finance.read"><span>ler</span></FinanceGate>
        <FinanceGate verb="finance.write"><span>escrever</span></FinanceGate>
      </>,
    )
    await waitFor(() => expect(screen.getByText("ler")).toBeInTheDocument())
    expect(screen.queryByText("escrever")).toBeNull()
  })
})
