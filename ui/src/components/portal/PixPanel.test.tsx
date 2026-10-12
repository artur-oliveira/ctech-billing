import {screen} from "@testing-library/react"
import {describe, expect, it, vi} from "vitest"

import {renderWithQuery} from "@/components/finance/finance.test-utils"

import {PixPanel} from "./PixPanel"

describe("PixPanel", () => {
  it("shows the last day to pay for a Pix with due date instead of a countdown", () => {
    renderWithQuery(<PixPanel settled={false} amount={1000} currency="BRL" onPaid={vi.fn()} onRegenerate={vi.fn()} regenerating={false}
      payment={{method: "pix", pix_code: "00020126FAKE", expires_at: "2099-05-02T03:00:00Z", payable_until: "2099-05-01"}}/>)
    expect(screen.getByText("pague até 01/05/2099")).toBeTruthy()
    expect(screen.queryByText(/expira em/)).toBeNull()
  })
})
