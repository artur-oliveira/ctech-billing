import {FINANCE_HREF} from "@/lib/finance/href";

/**
 * The last product (area) the person used, so the default entry (the landing
 * button, the sign-in page) opens it instead of always the portal.
 *
 * Only the default entry reads it: a deep link, an OAuth `returnTo` and the
 * "not yours" redirects stay exactly as they are. Access is not decided here
 * either: Finanças is everyone's, the console records itself only once the
 * person has an organization, and the console still sends a person with none
 * to Finanças. The stored value is checked against PRODUCT_HOME, so anything
 * else in localStorage falls back to the portal.
 */
export const PRODUCT_HOME = {
  portal: "/dashboard",
  console: "/console/overview",
  finance: FINANCE_HREF,
} as const;

export type Product = keyof typeof PRODUCT_HOME

const KEY = "ctech-billing-last-product";

const isProduct = (v: unknown): v is Product =>
  typeof v === "string" && Object.prototype.hasOwnProperty.call(PRODUCT_HOME, v);

export function rememberProduct(product: Product) {
  try {
    window.localStorage.setItem(KEY, product);
  } catch {
    // Blocked storage only loses the preference; nothing depends on it.
  }
}

/** Where the default entry goes: the last product's home, the portal when unknown. */
export function entryHref(): string {
  try {
    const v = typeof window === "undefined" ? null : window.localStorage.getItem(KEY);
    return isProduct(v) ? PRODUCT_HOME[v] : PRODUCT_HOME.portal;
  } catch {
    return PRODUCT_HOME.portal;
  }
}
