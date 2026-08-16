"use client"

import {QueryClient, QueryClientProvider} from "@tanstack/react-query"
import {useState} from "react"
import {Toaster} from "sonner"

import {MockControls} from "@/dev/MockControls"
import {AuthProvider} from "@/lib/auth/AuthContext"

export function Providers({children}: {children: React.ReactNode}) {
  // Created in state, not at module scope: a module-level client is shared
  // across requests on the server and leaks one reader's invoices into
  // another's cache.
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30_000,
            // A 404 on an invoice is an answer, not a hiccup. Retrying it
            // three times only delays the empty state by a second and a half.
            retry: (failureCount, error) => {
              const status = (error as {response?: {status?: number}})?.response?.status
              if (status && status >= 400 && status < 500) return false
              return failureCount < 2
            },
          },
        },
      })
  )

  return (
    <QueryClientProvider client={queryClient}>
      {/* Inside the query client, not outside: AuthProvider's boot-time refresh
          registers the function the axios client retries 401s with, and a query
          that fires before that registration would fail unrecoverably. */}
      <AuthProvider>{children}</AuthProvider>
      <Toaster position="top-center" richColors closeButton />
      <MockControls />
    </QueryClientProvider>
  )
}
