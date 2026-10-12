import {fireEvent, screen} from "@testing-library/react"

import type {Connection, ConnectionsScope, ProviderInfo} from "@/lib/api/connectionsTypes"

export const live: ConnectionsScope = {org: "01a04ed6-1af9-745e-bcf7-d3b66fe52321", mode: "live"}
export const test: ConnectionsScope = {...live, mode: "test"}

export const interInfo: ProviderInfo = {
  provider: "inter",
  fields: [{name: "client_id", required: true}],
  scopes: {statement: ["extrato.read"], collection: ["cobv.read", "cobv.write", "pix.read", "pix.write", "webhook.read", "webhook.write"]},
}

export const active: Connection = {
  provider: "inter", state: "active", status: "active", kind: "merchant", livemode: true, client_id: "cid-123",
  account_last4: "4567", cert_subject: "CN=ACME", cert_fingerprint: "ab12", cert_not_after: "2027-10-12T00:00:00Z",
  cert_days_left: 366, capabilities: ["statement", "collection"], missing_scopes: [], last_sync_ok_at: "2026-10-11T12:00:00Z",
  ledger_account_id: "byok-inter-abc", backfill_done: true, first_day: "2026-07-11", connected_at: "2026-10-10T12:00:00Z",
  notify_email: "financeiro@example.com",
}

/** Fills every field of the connect form and drops a valid pair of files. */
export function fillValidForm() {
  fireEvent.change(screen.getByLabelText("Client ID"), {target: {value: "cid-123"}})
  fireEvent.change(screen.getByLabelText("Client secret"), {target: {value: "s3cr3t"}})
  fireEvent.change(screen.getByLabelText("Conta corrente"), {target: {value: "1234567"}})
  fireEvent.change(screen.getByLabelText("E-mail para avisos"), {target: {value: "financeiro@example.com"}})
  fireEvent.drop(screen.getByTestId("file-drop-connect-cert"), {dataTransfer: {files: [new File(["-----BEGIN CERTIFICATE-----"], "inter.crt")]}})
  fireEvent.drop(screen.getByTestId("file-drop-connect-key"), {dataTransfer: {files: [new File(["-----BEGIN PRIVATE KEY-----"], "inter.key")]}})
  fireEvent.click(screen.getByRole("checkbox"))
}
