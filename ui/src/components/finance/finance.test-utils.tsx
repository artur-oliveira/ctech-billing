import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {expect} from "vitest"
import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render} from "@testing-library/react"
import type {ReactElement} from "react"

export function renderWithQuery(ui: ReactElement) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}, mutations: {retry: false}}})
  return {client, ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)}
}


/** The styled Select's trigger, found by its label like a form field. */
export function selectByLabel(label: string | RegExp, root: HTMLElement = document.body) {
  return within(root).getByRole("combobox", {name: label})
}

/** Opens a Select and returns the LABELS of its options (what a person sees). */
export async function optionsOf(label: string | RegExp, root?: HTMLElement): Promise<string[]> {
  await userEvent.click(selectByLabel(label, root))
  const names = (await screen.findAllByRole("option")).map(o => o.textContent?.trim() ?? "")
  await userEvent.keyboard("{Escape}")
  return names
}

/** Chooses an option by its label. */
export async function pick(label: string | RegExp, option: string, root?: HTMLElement) {
  await userEvent.click(selectByLabel(label, root))
  await userEvent.click(await screen.findByRole("option", {name: option}))
}

/**
 * Opens a Select once and waits for its options to become `expected` (what a
 * person sees once data has loaded). The poll only reads the open list: putting
 * `optionsOf` inside waitFor re-opens and closes the popup on every retry, which
 * under load outlasts waitFor's own timeout and made the test flaky.
 */
export async function expectOptionsEventually(label: string | RegExp, expected: string[], root?: HTMLElement) {
  await userEvent.click(selectByLabel(label, root))
  await waitFor(() => {
    expect(screen.getAllByRole("option").map(o => o.textContent?.trim() ?? "")).toEqual(expected)
  })
  await userEvent.keyboard("{Escape}")
}
