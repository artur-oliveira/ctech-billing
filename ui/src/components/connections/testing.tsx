import {fireEvent, screen} from "@testing-library/react";

import type {Connection, ConnectionsScope, ProviderInfo} from "@/lib/api/connectionsTypes";

export const live: ConnectionsScope = {org: "01a04ed6-1af9-745e-bcf7-d3b66fe52321", mode: "live"};
export const test: ConnectionsScope = {...live, mode: "test"};

export const interInfo: ProviderInfo = {
  provider: "inter",
  fields: [{name: "client_id", required: true}],
  guide: {
    name_max: 50, description_max: 250, suggested_name: "CTech Billing", accept_terms: true,
    activities: [
      {code: "pix", permissions: [
        {key: "due_charge_write", scopes: ["cobv.write"]}, {key: "due_charge_read", scopes: ["cobv.read"]},
        {key: "pix_refund", scopes: ["pix.write"]}, {key: "pix_read", scopes: ["pix.read"]},
        {key: "webhook_write", scopes: ["webhook.write"]}, {key: "webhook_read", scopes: ["webhook.read"]},
      ]},
      {code: "recurring", permissions: [{key: "recurrence_read", scopes: []}, {key: "recurrence_write", scopes: []}]},
      {code: "collection", permissions: [{key: "boleto_read", scopes: []}, {key: "boleto_write", scopes: []}]},
      {code: "payments", permissions: [{key: "banking_webhook_write", scopes: []}, {key: "banking_webhook_read", scopes: []}]},
      {code: "statement", permissions: [{key: "statement_read", scopes: ["extrato.read"]}]},
    ],
  },
};

export const active: Connection = {
  provider: "inter", state: "active", status: "active", kind: "merchant", livemode: true, client_id: "cid-123",
  account_last4: "4567", cert_subject: "CN=ACME", cert_fingerprint: "ab12", cert_not_after: "2027-10-12T00:00:00Z",
  cert_days_left: 366, capabilities: ["statement", "collection"], missing_scopes: [], last_sync_ok_at: "2026-10-11T12:00:00Z",
  ledger_account_id: "byok-inter-abc", backfill_done: true, first_day: "2026-07-11", connected_at: "2026-10-10T12:00:00Z",
  notify_email: "financeiro@example.com",
};

/** Fills every field of the connect form and drops a valid pair of files. */
export function fillValidForm() {
  fireEvent.change(screen.getByLabelText("Client ID"), {target: {value: "cid-123"}});
  fireEvent.change(screen.getByLabelText("Client secret"), {target: {value: "s3cr3t"}});
  fireEvent.change(screen.getByLabelText("Conta corrente"), {target: {value: "1234567"}});
  fireEvent.change(screen.getByLabelText("E-mail para avisos"), {target: {value: "financeiro@example.com"}});
  fireEvent.drop(screen.getByTestId("file-drop-connect-cert"), {dataTransfer: {files: [new File(["-----BEGIN CERTIFICATE-----"], "inter.crt")]}});
  fireEvent.drop(screen.getByTestId("file-drop-connect-key"), {dataTransfer: {files: [new File(["-----BEGIN PRIVATE KEY-----"], "inter.key")]}});
  fireEvent.click(screen.getByRole("checkbox"));
}
