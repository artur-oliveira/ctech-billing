import "@testing-library/jest-dom/vitest"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render, screen, within} from "@testing-library/react"
import type {ReactNode} from "react"
import {afterEach, describe, expect, it, vi} from "vitest"

import ConsoleCustomerPage from "@/app/(console)/console/customer/page"
import ConsoleSubscriptionsPage from "@/app/(console)/console/subscriptions/page"
import * as consoleApi from "@/lib/api/console"
import type {ConsoleSubscriptionRow} from "@/lib/api/consoleTypes"

vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams("id=cus_1"),
  useRouter: () => ({push: vi.fn()}),
  usePathname: () => "/console/customer",
}))

afterEach(() => vi.restoreAllMocks())

function wrap(ui: ReactNode) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

function row(over: Partial<ConsoleSubscriptionRow> = {}): ConsoleSubscriptionRow {
  return {
    id: "sub_1", customer_id: "cus_1", status: "ACTIVE", entitled: true,
    recurrence: {interval: "month", count: 1}, billing_timing: "advance", anchor: "2026-10-10",
    current_period: {period_start: "2026-10-10", period_end: "2026-11-10"},
    cancel_at_period_end: false, livemode: true,
    customer_name: "A O CARVALHO TECH", plan_name: "DF-e Ilimitado interno",
    ...over,
  }
}

// An operator reads a subscription by its plan and its customer; sub_… and
// cus_… answered neither (the owner, 2026-10-10).
describe("console subscription labels", () => {
  it("lists a subscription by its plan, linked to its detail, and its customer by name", async () => {
    vi.spyOn(consoleApi, "listConsoleSubscriptions").mockResolvedValue({data: [row()], has_more: false})
    wrap(<ConsoleSubscriptionsPage/>)

    const table = await screen.findByRole("table")
    const plan = within(table).getByRole("link", {name: "DF-e Ilimitado interno"})
    expect(plan).toHaveAttribute("href", "/console/subscription?id=sub_1")
    expect(within(table).getByRole("link", {name: "A O CARVALHO TECH"}))
      .toHaveAttribute("href", "/console/customer?id=cus_1")
    expect(within(table).queryByText("sub_1")).not.toBeInTheDocument()
    expect(within(table).queryByText("cus_1")).not.toBeInTheDocument()
  })

  it("has a phone layout that names the plan and the customer too", async () => {
    vi.spyOn(consoleApi, "listConsoleSubscriptions").mockResolvedValue({data: [row()], has_more: false})
    wrap(<ConsoleSubscriptionsPage/>)

    const list = await screen.findByRole("list", {name: "Assinaturas"})
    expect(within(list).getByRole("link", {name: "DF-e Ilimitado interno"}))
      .toHaveAttribute("href", "/console/subscription?id=sub_1")
    expect(within(list).getByText("A O CARVALHO TECH")).toBeInTheDocument()
  })

  it("falls back to the ids when the names could not be read", async () => {
    vi.spyOn(consoleApi, "listConsoleSubscriptions").mockResolvedValue({
      data: [row({customer_name: undefined, plan_name: undefined})], has_more: false,
    })
    wrap(<ConsoleSubscriptionsPage/>)

    const table = await screen.findByRole("table")
    expect(within(table).getByRole("link", {name: "sub_1"}))
      .toHaveAttribute("href", "/console/subscription?id=sub_1")
    expect(within(table).getByRole("link", {name: "cus_1"})).toBeInTheDocument()
  })

  it("names the plan on the customer's subscriptions", async () => {
    vi.spyOn(consoleApi, "getConsoleCustomer").mockResolvedValue({
      customer: {id: "cus_1", name: "A O CARVALHO TECH", anonymized: false, livemode: true},
      subscriptions: [row({plan_name: "Finanças Free"})],
      timeline: [],
    })
    wrap(<ConsoleCustomerPage/>)

    const link = await screen.findByRole("link", {name: "Finanças Free"})
    expect(link).toHaveAttribute("href", "/console/subscription?id=sub_1")
    expect(screen.queryByText("sub_1")).not.toBeInTheDocument()
  })
})
