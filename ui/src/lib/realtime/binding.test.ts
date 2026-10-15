import {QueryClient, type QueryKey} from "@tanstack/react-query";
import {describe, expect, it} from "vitest";

import {checkoutKeys} from "@/lib/api/checkout";
import {connectionKeys} from "@/lib/api/connections";
import {consoleKeys} from "@/lib/api/console";
import {financeKeys} from "@/lib/api/finance";
import {portalKeys} from "@/lib/api/portal";

import {applyChange, applyGap} from "./binding";
import type {Surface} from "./catalogue";
import type {ChangeView} from "./codec";

const ORG_A = "01a04ed6-1af9-745e-bcf7-d3b66fe52321";
const ORG_B = "01b04ed6-1af9-745e-bcf7-d3b66fe52322";
const scopeA = {org: ORG_A, mode: "live"} as const;
const scopeATest = {org: ORG_A, mode: "test"} as const;
const scopeB = {org: ORG_B, mode: "live"} as const;
const spaceA = {kind: "organization", organizationId: ORG_A} as const;
const spaceB = {kind: "organization", organizationId: ORG_B} as const;

const change = (entity: string, type: string, entityId = "x_1"): ChangeView => ({
  id: "01A", type, entity, entityId, status: "PAID", revision: 1, occurredAtMs: 1,
});

/** A client with one cached query per key, none observed. */
function seeded(keys: QueryKey[]): QueryClient {
  const qc = new QueryClient();
  for (const key of keys) qc.setQueryData(key, {seeded: true});
  return qc;
}

const stale = (qc: QueryClient, key: QueryKey) => qc.getQueryState(key)?.isInvalidated === true;

const consoleKeysA = {
  invoices: consoleKeys.invoiceMonth(scopeA, 2026, 10),
  detail: consoleKeys.invoice(scopeA, "in_1"),
  otherDetail: consoleKeys.invoice(scopeA, "in_2"),
  overview: consoleKeys.overview(scopeA, 2026, 10),
  customers: consoleKeys.customers(scopeA),
  subscriptions: consoleKeys.subscriptions(scopeA),
  settings: consoleKeys.settings(scopeA),
  // Not this scope's:
  otherOrg: consoleKeys.invoiceMonth(scopeB, 2026, 10),
  otherMode: consoleKeys.invoiceMonth(scopeATest, 2026, 10),
};

describe("applyChange on the console", () => {
  it("an invoice change refreshes this scope's invoices and overview, and nothing of another org or mode", () => {
    const qc = seeded(Object.values(consoleKeysA));
    applyChange(qc, {kind: "console", scope: scopeA}, change("invoice", "invoice.paid", "in_1"));
    expect(stale(qc, consoleKeysA.invoices)).toBe(true);
    expect(stale(qc, consoleKeysA.detail)).toBe(true);
    expect(stale(qc, consoleKeysA.overview)).toBe(true);
    expect(stale(qc, consoleKeysA.customers)).toBe(false);
    expect(stale(qc, consoleKeysA.settings)).toBe(false);
    expect(stale(qc, consoleKeysA.otherOrg)).toBe(false);
    expect(stale(qc, consoleKeysA.otherMode)).toBe(false);
  });

  it("a payer recorded refreshes that invoice's detail and not another's, nor the list", () => {
    const qc = seeded(Object.values(consoleKeysA));
    applyChange(qc, {kind: "console", scope: scopeA}, change("invoice", "invoice.payer_recorded", "in_1"));
    expect(stale(qc, consoleKeysA.detail)).toBe(true);
    expect(stale(qc, consoleKeysA.otherDetail)).toBe(false);
    expect(stale(qc, consoleKeysA.invoices)).toBe(false);
  });

  it("a customer and a subscription each refresh their own screens", () => {
    const qc = seeded(Object.values(consoleKeysA));
    applyChange(qc, {kind: "console", scope: scopeA}, change("customer", "customer.updated", "cus_1"));
    expect(stale(qc, consoleKeysA.customers)).toBe(true);
    expect(stale(qc, consoleKeysA.subscriptions)).toBe(false);
    applyChange(qc, {kind: "console", scope: scopeA}, change("subscription", "subscription.activated", "sub_1"));
    expect(stale(qc, consoleKeysA.subscriptions)).toBe(true);
    expect(stale(qc, consoleKeysA.invoices)).toBe(false);
  });

  it("a connection change refreshes the connections of this org and mode only", () => {
    const keys = [connectionKeys.list({org: ORG_A, mode: "live"}), connectionKeys.one({org: ORG_A, mode: "live"}, "inter"),
      connectionKeys.list({org: ORG_B, mode: "live"}), connectionKeys.list({org: ORG_A, mode: "test"})];
    const qc = seeded(keys);
    applyChange(qc, {kind: "console", scope: scopeA}, change("connection", "connection.updated", "inter"));
    expect(stale(qc, keys[0])).toBe(true);
    expect(stale(qc, keys[1])).toBe(true); // under the same prefix
    expect(stale(qc, keys[2])).toBe(false);
    expect(stale(qc, keys[3])).toBe(false);
  });
});

describe("applyChange on the portal", () => {
  it("an invoice change refreshes the portal's invoices (list and detail) and not its subscriptions", () => {
    const keys = [portalKeys.invoiceList, portalKeys.invoice("in_1"), portalKeys.subscriptions, portalKeys.session];
    const qc = seeded(keys);
    applyChange(qc, {kind: "portal"}, change("invoice", "invoice.paid", "in_1"));
    expect(stale(qc, keys[0])).toBe(true);
    expect(stale(qc, keys[1])).toBe(true);
    expect(stale(qc, keys[2])).toBe(false);
    expect(stale(qc, keys[3])).toBe(false);
  });
});

describe("applyChange on Finanças", () => {
  it("a bank line refreshes this space and mode, never another space or mode, nor the console", () => {
    const keys = [
      financeKeys.bills("live", spaceA, "payable"), financeKeys.accounts("live", spaceA),
      connectionKeys.unclassified({mode: "live", space: spaceA}, "acc"),
      financeKeys.bills("live", spaceB, "payable"), financeKeys.bills("test", spaceA, "payable"),
      consoleKeys.invoices(scopeA),
    ];
    const qc = seeded(keys);
    applyChange(qc, {kind: "finance", mode: "live", space: spaceA}, change("statement", "statement.line_posted", "tx_1"));
    expect(keys.slice(0, 3).every((k) => stale(qc, k))).toBe(true);
    expect(keys.slice(3).some((k) => stale(qc, k))).toBe(false);
  });
});

describe("applyChange on the checkout", () => {
  it("refreshes this link's view and no other link's, and never writes a status into it", () => {
    const qc = seeded([checkoutKeys.view("tok"), checkoutKeys.view("another")]);
    applyChange(qc, {kind: "checkout", token: "tok"}, change("invoice", "invoice.paid", "in_1"));
    expect(stale(qc, checkoutKeys.view("tok"))).toBe(true);
    expect(stale(qc, checkoutKeys.view("another"))).toBe(false);
    expect(qc.getQueryData(checkoutKeys.view("tok"))).toEqual({seeded: true}); // invalidated, never patched
  });
});

describe("applyGap", () => {
  it("invalidates everything under the surface's root and nothing under another", () => {
    const keys = [
      ...Object.values(consoleKeysA).slice(0, 7),
      connectionKeys.list({org: ORG_A, mode: "live"}),
      consoleKeys.invoiceMonth(scopeB, 2026, 10), consoleKeys.invoiceMonth(scopeATest, 2026, 10),
      portalKeys.invoices, financeKeys.all("live", spaceA), checkoutKeys.view("tok"),
    ];
    const qc = seeded(keys);
    applyGap(qc, {kind: "console", scope: scopeA});
    expect(keys.slice(0, 8).every((k) => stale(qc, k))).toBe(true);
    expect(keys.slice(8).some((k) => stale(qc, k))).toBe(false);
  });

  it("on the portal it touches the portal only", () => {
    const keys = [portalKeys.invoices, portalKeys.subscriptions, consoleKeys.invoices(scopeA), financeKeys.all("live", spaceA)];
    const qc = seeded(keys);
    applyGap(qc, {kind: "portal"} satisfies Surface);
    expect(stale(qc, keys[0]) && stale(qc, keys[1])).toBe(true);
    expect(stale(qc, keys[2]) || stale(qc, keys[3])).toBe(false);
  });
});
