import {t, i18nKeyExists} from "@/lib/i18n"
import {money, shortDate} from "@/lib/format"

/** One field-level failure, as the API's problem+json `errors[]` carries it. */
export interface FieldError {
  field: string
  code: string
  /** English fallback for logs, never for a reader. */
  message?: string
  params?: Record<string, string | number | (string | number)[]>
}

const STATUS_CODE: Record<number, string> = {
  400: "bad_request",
  401: "unauthorized",
  403: "forbidden",
  404: "not_found",
  409: "conflict",
  422: "unprocessable_entity",
  429: "too_many_requests",
}

/** Fields whose numeric params are centavos. */
const AMOUNT_FIELD = /(^|[._])(amount|total|price|balance|value|unit_amount|difference)([._]|$)/i
const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/

function formatParam(field: string, code: string, key: string, value: unknown): unknown {
  if (Array.isArray(value)) return value.join(", ")
  if ((code === "out_of_range" || code === "too_large") && typeof value === "number" && AMOUNT_FIELD.test(field)
    && (key === "min" || key === "max")) return money(value)
  if (typeof value === "string" && ISO_DATE.test(value)) return shortDate(value)
  return value
}

/** Reader-facing text for one field error, from its code and params. */
export function fieldErrorMessage(err: FieldError): string {
  const params: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(err.params ?? {})) params[k] = formatParam(err.field, err.code, k, v)
  const key = `errors.field.${err.code}`
  return t(i18nKeyExists(key) ? key : "errors.field.invalid", params)
}

/** Reader-facing text for a problem `code`; unknown codes fall back to a generic message. */
export function codeMessage(code: string | undefined, status?: number): string {
  if (code && i18nKeyExists(`errors.code.${code}`)) return t(`errors.code.${code}`)
  if (!code && status && STATUS_CODE[status]) return t(`errors.code.${STATUS_CODE[status]}`)
  return t("auth.errors.generic")
}
