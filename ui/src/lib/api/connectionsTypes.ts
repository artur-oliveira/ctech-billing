/** Bank connections (BYOK spec 2026-10-11 § 11, § 12.1). Shapes of the API. */

export type ConnectionState = "onboarding" | "active" | "expired" | "disconnected"
export type ConnectionStatus = "onboarding" | "active" | "degraded" | "expired" | "disconnected"
export type OnboardingStep = "requirements" | "create_integration" | "validation" | "download" | "upload" | "webhook"
export type Capability = "statement" | "collection"

export const ONBOARDING_STEPS: OnboardingStep[] = ["requirements", "create_integration", "validation", "download", "upload", "webhook"]

export interface Connection {
  provider: "inter"
  state: ConnectionState
  status: ConnectionStatus
  kind?: "merchant" | "sandbox_shared"
  livemode: boolean
  client_id?: string
  account_last4?: string
  pix_key?: string
  cert_subject?: string
  cert_fingerprint?: string
  cert_not_after?: string
  cert_days_left?: number
  capabilities: Capability[]
  missing_scopes: string[]
  webhook_status?: string
  last_sync_ok_at?: string
  last_error_code?: string
  last_error_at?: string
  onboarding_step?: OnboardingStep
  onboarding_since?: string
  ledger_account_id?: string
  backfill_done: boolean
  first_day?: string
  connected_at?: string
  notify_email?: string
}

export interface ProviderField {
  name: string
  secret?: boolean
  file?: boolean
  required: boolean
}

export interface ProviderInfo {
  provider: "inter"
  fields: ProviderField[]
  scopes: {statement: string[]; collection: string[]}
}

export interface ConnectForm {
  client_id: string
  client_secret: string
  account: string
  pix_key: string
  notify_email: string
  backfill_months: number
  authorized: boolean
  cert: File
  key: File
}

export interface ConnectionsScope {
  org: string
  mode: "live" | "test"
}

export interface SyncResult {
  posted: number
  skipped: number
  backfill_done: boolean
}

export interface UnclassifiedLine {
  tx_id: string
  date: string
  amount: number
  direction: "credit" | "debit"
  memo?: string
}

export interface ClassifyBody {
  category_id?: string
  bill_id?: string
  transfer_account_id?: string
}
