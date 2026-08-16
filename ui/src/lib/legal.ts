/**
 * Where the legal documents live — and they do not live here.
 *
 * ctech-account hosts the legal centre for every CTech product, and billing
 * links into it exactly as ctech-dfe and ctech-wallet do (their `lib/legal.ts`
 * is this file). One company, one place a person reads what they agreed to: a
 * service that ships its own copy of the privacy policy is a service whose copy
 * is eventually the stale one, and nobody finds out from the copy.
 *
 * The host is read from `NEXT_PUBLIC_CTECH_CLIENT_URL` — the same variable the
 * OAuth round trip already uses, so a dev or stage build links at the accounts
 * app it actually signs in against rather than at production. The siblings
 * hardcode it; this costs nothing and is right in three environments instead of
 * one.
 */
const ACCOUNTS = process.env.NEXT_PUBLIC_CTECH_CLIENT_URL || "https://accounts.aoctech.app"

/** The index of every policy, for a footer. */
export const ACCOUNTS_LEGAL_URL = `${ACCOUNTS}/legal`

/**
 * The billing-specific terms addendum — what the gate asks about.
 *
 * It is a *product* page under the accounts app, not a general terms page:
 * signing up through SSO never presents a checkbox for terms specific to a
 * product somebody had not opened yet, which is the entire reason the gate
 * exists.
 */
export const BILLING_TERMS_URL = `${ACCOUNTS}/products/billing`

export const PRIVACY_POLICY_URL = `${ACCOUNTS}/privacy`
