import "@testing-library/jest-dom/vitest"

import {useQuery} from "@tanstack/react-query"
import {act, screen} from "@testing-library/react"
import {afterEach, describe, expect, it} from "vitest"

import {renderWithQuery} from "@/components/finance/finance.test-utils"
import {financeKeys} from "@/lib/api/finance"
import {setSpace} from "@/lib/console/space"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"

const ACME = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

afterEach(() => window.localStorage.clear())

// Review Focus 1: switching space must never render the other space's rows.
function Rows({answers}: {answers: Record<string, Promise<string[]>>}) {
  const ctx = useFinanceCtx()
  const key = financeKeys.accounts(ctx.mode, ctx.space)
  const q = useQuery({queryKey: key, queryFn: () => answers[key[2]]})
  if (!q.data) return <p>carregando</p>
  return <ul>{q.data.map(r => <li key={r}>{r}</li>)}</ul>
}

describe("the space in the query key", () => {
  it("never renders the previous space's rows while the next one loads", async () => {
    let resolveOrg: (v: string[]) => void = () => undefined
    const answers = {
      personal: Promise.resolve(["conta-pessoal"]),
      [`org:${ACME}`]: new Promise<string[]>(r => (resolveOrg = r)),
    }
    renderWithQuery(<Rows answers={answers}/>)
    expect(await screen.findByText("conta-pessoal")).toBeInTheDocument()

    act(() => setSpace({kind: "organization", organizationId: ACME}))
    expect(screen.queryByText("conta-pessoal")).toBeNull()
    expect(screen.getByText("carregando")).toBeInTheDocument()

    await act(async () => resolveOrg(["conta-acme"]))
    expect(await screen.findByText("conta-acme")).toBeInTheDocument()
  })
})
