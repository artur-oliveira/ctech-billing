import {screen, waitFor, within} from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type {AxiosRequestConfig} from "axios"
import {expect, vi} from "vitest"
import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render} from "@testing-library/react"
import type {ReactElement} from "react"

import {financeMock, resetFinanceMock} from "@/dev/financeMockData"
import {apiClient} from "@/lib/api/client"

export interface SentRequest {
  method: string
  url: string
  data: unknown
}

/**
 * The whole stack under a screen: its calls go through lib/api/finance (headers,
 * keys, the PATCH body rule) into the dev mock, which keeps state like the API.
 * Returns every request sent, as sent, so a test can read what crossed the wire
 * and then what a reload shows.
 */
export function serveFinanceMock(): SentRequest[] {
  resetFinanceMock()
  const sent: SentRequest[] = []
  vi.spyOn(apiClient, "request").mockImplementation(async (config: AxiosRequestConfig) => {
    const method = (config.method ?? "get").toLowerCase()
    sent.push({method, url: config.url ?? "", data: config.data})
    const res = financeMock({method, url: config.url ?? "", headers: config.headers as Record<string, unknown>, data: config.data, params: config.params as Record<string, unknown>})
    if (res.status >= 400) throw Object.assign(new Error(`HTTP ${res.status}`), {isAxiosError: true, response: {status: res.status, data: res.data}})
    return {data: res.data, status: res.status, statusText: "OK", headers: {}, config} as never
  })
  return sent
}

/** The last write of `method` to a path ending in `suffix`, as sent. */
export function lastWrite(sent: SentRequest[], method: string, suffix: string): Record<string, unknown> {
  const hit = sent.filter(r => r.method === method && r.url.endsWith(suffix)).at(-1)
  if (!hit) throw new Error(`no ${method} …${suffix} was sent`)
  return hit.data as Record<string, unknown>
}

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
