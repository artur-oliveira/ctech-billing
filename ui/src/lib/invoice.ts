import {t} from "@/lib/i18n"

/**
 * The line shown for an invoice wherever it is listed or titled.
 *
 * The API sends the first line's raw description ("" when there are no lines)
 * and how many more follow; the words joining them are the catalog's.
 */
export function invoiceTitle(invoice: { description: string; extra_lines?: number }): string {
  const description = invoice.description.trim()
  if (description === "") return t("portal.invoice.untitled")
  const extra = invoice.extra_lines ?? 0
  return extra > 0 ? t("portal.invoice.title", {description, count: extra}) : description
}
