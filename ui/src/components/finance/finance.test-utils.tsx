import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {render} from "@testing-library/react"
import type {ReactElement} from "react"

export function renderWithQuery(ui: ReactElement) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}, mutations: {retry: false}}})
  return {client, ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)}
}
