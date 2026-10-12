import {fireEvent, screen, waitFor} from "@testing-library/react"
import {describe, expect, it, vi} from "vitest"

import {renderWithQuery} from "@/components/finance/finance.test-utils"
import * as api from "@/lib/api/connections"
import type {Connection} from "@/lib/api/connectionsTypes"

import {InterOnboarding} from "./InterOnboarding"
import {interInfo, live} from "./testing"

describe("InterOnboarding", () => {
  it("starts at the saved step and offers the copyable scope list with the no-payments warning", () => {
    renderWithQuery(<InterOnboarding scope={live} connection={{provider: "inter", state: "onboarding", onboarding_step: "create_integration"} as Connection} providers={[interInfo]}/>)
    expect(screen.getByText("Criar a integração no Inter").closest("li")!.getAttribute("aria-current")).toBe("step")
    expect(screen.getByText("extrato.read cobv.read cobv.write pix.read pix.write webhook.read webhook.write")).toBeTruthy()
    expect(screen.getByText("Não marque nenhum escopo de pagamento (pagamento-*).")).toBeTruthy()
  })

  it("starts at the requirements when nothing was saved, and saves the next step", async () => {
    const save = vi.spyOn(api, "setOnboarding").mockResolvedValue()
    renderWithQuery(<InterOnboarding scope={live} connection={null} providers={[interInfo]}/>)
    expect(screen.getByText("Requisitos").closest("li")!.getAttribute("aria-current")).toBe("step")
    fireEvent.click(screen.getByRole("button", {name: "Tenho uma conta PJ"}))
    await waitFor(() => expect(save).toHaveBeenCalledWith(live, "inter", "create_integration", expect.any(String)))
  })

  it("shows since when the person has been waiting for Inter's validation", () => {
    renderWithQuery(<InterOnboarding scope={live} providers={[interInfo]}
      connection={{provider: "inter", state: "onboarding", onboarding_step: "validation", onboarding_since: "2026-10-09T12:00:00Z"} as Connection}/>)
    expect(screen.getByText("Aguardando desde 09/10/2026")).toBeTruthy()
  })
})
