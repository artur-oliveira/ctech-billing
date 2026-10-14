import {fireEvent, render, screen} from "@testing-library/react"
import {describe, expect, it, vi} from "vitest"

import {PixKeyDialog} from "./PixKeyDialog"

function open(over: Partial<React.ComponentProps<typeof PixKeyDialog>> = {}) {
  const onSubmit = vi.fn()
  render(<PixKeyDialog open onClose={() => {}} onSubmit={onSubmit} loading={false} current="old-key" {...over}/>)
  return onSubmit
}

describe("PixKeyDialog", () => {
  it("explains why a key of its own is the right one", () => {
    open()
    expect(screen.getByText(/chave aleatória/i)).toBeTruthy()
  })

  it("sends the trimmed key", () => {
    const onSubmit = open()
    fireEvent.change(screen.getByLabelText("Nova chave Pix"), {target: {value: "  new-evp-key  "}})
    fireEvent.click(screen.getByRole("button", {name: "Alterar chave"}))
    expect(onSubmit).toHaveBeenCalledWith("new-evp-key")
  })

  it("will not send a blank key or the one already in use", () => {
    const onSubmit = open()
    const button = screen.getByRole("button", {name: "Alterar chave"}) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.change(screen.getByLabelText("Nova chave Pix"), {target: {value: "old-key"}})
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("shows the reason it was refused", () => {
    open({error: "Há 2 cobranças em aberto"})
    expect(screen.getByRole("alert").textContent).toContain("Há 2 cobranças em aberto")
  })
})
