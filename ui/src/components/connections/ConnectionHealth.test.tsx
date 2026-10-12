import {fireEvent, screen} from "@testing-library/react"
import {describe, expect, it} from "vitest"

import {renderWithQuery} from "@/components/finance/finance.test-utils"

import {ConnectionHealth} from "./ConnectionHealth"
import {active, live} from "./testing"

describe("ConnectionHealth", () => {
  it("warns from 90 days before expiry", () => {
    renderWithQuery(<ConnectionHealth scope={live} connection={{...active, cert_days_left: 89, cert_not_after: "2027-01-08T12:00:00Z"}}/>)
    // A warning is a status (polite), not an alert: nothing failed yet.
    expect(screen.getByRole("status").textContent).toContain("vence em 08/01/2027")
  })

  it("is quiet with a year left", () => {
    renderWithQuery(<ConnectionHealth scope={live} connection={active}/>)
    expect(screen.queryByRole("status")).toBeNull()
  })

  it("says in words which scopes collection lacks", () => {
    renderWithQuery(<ConnectionHealth scope={live} connection={{...active, capabilities: ["statement"], missing_scopes: ["cobv.write", "pix.write"]}}/>)
    expect(screen.getByText(/faltam estes escopos na integração: cobv.write, pix.write/)).toBeTruthy()
  })

  it("names the status with a word, never colour alone", () => {
    renderWithQuery(<ConnectionHealth scope={live} connection={{...active, status: "degraded"}}/>)
    expect(screen.getByText("Atenção")).toBeTruthy()
  })

  it("asks for confirmation and tells only the merchant can cancel at Inter", async () => {
    renderWithQuery(<ConnectionHealth scope={live} connection={active}/>)
    fireEvent.click(screen.getByRole("button", {name: "Desconectar"}))
    expect(await screen.findByText(/Só você pode cancelar a integração no Inter/)).toBeTruthy()
  })
})
