import {beforeEach, describe, expect, it, vi} from "vitest"

import {apiClient, messageFor} from "@/lib/api/client"
import {getConsoleInvoicePDF} from "@/lib/api/console"
import {getInvoicePDF} from "@/lib/api/portal"
import en from "@/locales/en"
import ptBR from "@/locales/pt-BR"
import {changeAppLanguage} from "@/lib/i18n"
import {fieldErrorMessage} from "@/lib/errors"
import {invoiceTitle} from "@/lib/invoice"

const problem = (status: number, data: object) => ({response: {status, data: {type: "about:blank", title: "T", status, ...data}}})

const BACKEND_CODES = [
  "invalid_body", "invalid_cursor", "invalid_link", "invalid_signature", "resource_not_found", "invoice_not_found",
  "subscription_not_found", "customer_tax_id_missing", "invoice_not_payable", "payment_unavailable",
  "charge_amount_rejected", "amount_exceeds_charge_ceiling", "subscription_no_billable_item", "invalid_credentials",
  "role_denied", "user_session_required", "no_organization", "service_token_required", "credential_not_enabled",
  "no_billing_account", "idempotency_key_required", "idempotency_key_too_long", "idempotency_conflict",
  "idempotency_tenant_unresolved", "idempotency_lookup_failed", "space_not_found", "space_unavailable",
  "organization_resolve_failed", "account_resolve_failed", "credential_resolve_failed", "internal_server_error",
  "invalid_transition", "concurrent_update", "payment_attempt_in_progress", "card_moved", "already_generated",
  "payout_not_enabled", "purchase_refunded", "statement_not_closable", "nothing_to_advance", "opening_balance_exists",
  "not_reversible_entry", "unknown_account", "invalid_card", "invalid_installments", "invalid_bill", "invalid_recurrence",
  "invalid_transaction", "invalid_account", "invalid_metadata", "invalid_price", "invalid_usage",
  "invalid_subscription_item", "invalid_credit_note", "invalid_dunning_policy", "invalid_invoice_items", "bad_request",
  "unauthorized", "forbidden", "not_found", "conflict", "unprocessable_entity", "too_many_requests", "validation_error",
  "line_already_reconciled", "line_bill_mismatch", "statement_card_not_supported", "statement_many_accounts", "statement_currency",
  "statement_too_large", "statement_empty", "statement_unreadable", "csv_mapping_required", "invalid_csv_mapping",
]
const FIELD_CODES = [
  "required", "too_long", "too_many", "too_large", "invalid_chars", "invalid_id", "invalid_email", "invalid_tax_id",
  "invalid_format", "invalid_expression", "not_found", "unsupported_value", "required_with", "same_account",
  "installment_too_small", "date_too_early", "date_too_late", "range_end_before_start", "range_too_long", "out_of_range",
]

beforeEach(() => changeAppLanguage("pt-BR"))

describe("error catalog", () => {
  it("covers every code the backend emits, in both languages", () => {
    for (const cat of [en, ptBR]) {
      for (const c of BACKEND_CODES) expect((cat.errors.code as Record<string, string>)[c], c).toBeTruthy()
      for (const c of FIELD_CODES) expect((cat.errors.field as Record<string, string>)[c], c).toBeTruthy()
    }
  })
})

describe("messageFor", () => {
  it("maps by code and never shows the English detail", () => {
    const msg = messageFor(problem(409, {code: "card_moved", detail: "card moved to another space"}))
    expect(msg).toBe(ptBR.errors.code.card_moved)
    expect(msg).not.toContain("another space")
  })

  it("falls back to a generic message for an unknown code", () => {
    expect(messageFor(problem(418, {code: "teapot", detail: "I am a teapot"}))).toBe(ptBR.auth.errors.generic)
  })

  it("shows the first field error of a validation problem", () => {
    const err = problem(422, {code: "validation_error", errors: [{field: "description", code: "too_long", message: "x", params: {max: 120}}]})
    expect(messageFor(err)).toBe("Use no máximo 120 caracteres.")
  })

  it("follows the UI language", async () => {
    await changeAppLanguage("en")
    expect(messageFor(problem(404, {code: "invoice_not_found"}))).toBe(en.errors.code.invoice_not_found)
  })
})

describe("fieldErrorMessage", () => {
  it("formats money params as currency for amount fields", () => {
    const msg = fieldErrorMessage({field: "amount", code: "out_of_range", params: {min: 1, max: 1000000}})
    expect(msg).toContain("R$")
    expect(msg).toContain("10.000,00")
  })

  it("leaves non-amount ranges as plain numbers", () => {
    expect(fieldErrorMessage({field: "installments", code: "out_of_range", params: {min: 1, max: 48}})).toBe("Use um valor entre 1 e 48.")
  })

  it("falls back for an unknown field code", () => {
    expect(fieldErrorMessage({field: "x", code: "brand_new"})).toBe(ptBR.errors.field.invalid)
  })
})

describe("invoiceTitle", () => {
  it("composes the first line and the extra count, in the UI language", async () => {
    expect(invoiceTitle({description: "Plano", extra_lines: 2})).toBe("Plano e mais 2")
    await changeAppLanguage("en")
    expect(invoiceTitle({description: "Plan", extra_lines: 2})).toBe("Plan and 2 more")
  })

  it("is the bare description with no extras, and generic when empty", () => {
    expect(invoiceTitle({description: "Plano"})).toBe("Plano")
    expect(invoiceTitle({description: ""})).toBe("Fatura")
  })
})

describe("invoice PDF language", () => {
  it("passes ?lang= on both shells", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {url: "u", expires_in: 1}})
    await getInvoicePDF("i1", "en")
    expect(get).toHaveBeenLastCalledWith("/v1.0/portal/invoices/i1/pdf", {params: {lang: "en"}})
    await getConsoleInvoicePDF("i1", "live", "pt-BR")
    expect(get).toHaveBeenLastCalledWith("/v1.0/console/invoices/i1/pdf", expect.objectContaining({params: {lang: "pt-BR"}}))
    get.mockRestore()
  })
})
