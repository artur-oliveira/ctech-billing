import {describe, expect, it} from "vitest";

import {checkoutKeys} from "@/lib/api/checkout";
import {connectionKeys} from "@/lib/api/connections";
import {consoleKeys} from "@/lib/api/console";
import {financeKeys} from "@/lib/api/finance";
import {portalKeys} from "@/lib/api/portal";

import {effectsOf, rootsOf, type Surface} from "./catalogue";
import type {ChangeView} from "./codec";

const ORG = "01a04ed6-1af9-745e-bcf7-d3b66fe52321";
const scope = {org: ORG, mode: "live"} as const;
const space = {kind: "organization", organizationId: ORG} as const;

const change = (entity: string, type: string, entityId = "x_1"): ChangeView => ({
  id: "01A", type, entity, entityId, status: "", revision: 1, occurredAtMs: 1,
});

const consoleSurface: Surface = {kind: "console", scope};
const portalSurface: Surface = {kind: "portal"};
const financeSurface: Surface = {kind: "finance", mode: "live", space};
const checkoutSurface: Surface = {kind: "checkout", token: "tok"};

describe("what a change touches, per surface", () => {
  it("an invoice: the console's invoices and overview, the portal's invoices, the checkout's view", () => {
    const c = change("invoice", "invoice.paid", "in_1");
    expect(effectsOf(consoleSurface, c).invalidate).toEqual([
      consoleKeys.invoices(scope), ["console", ORG, "live", "overview"],
    ]);
    expect(effectsOf(portalSurface, c).invalidate).toEqual([portalKeys.invoices]);
    expect(effectsOf(checkoutSurface, c).invalidate).toEqual([checkoutKeys.view("tok")]);
    expect(effectsOf(financeSurface, c).invalidate).toEqual([]);
  });

  it("a payer recorded refreshes that invoice's detail only", () => {
    const c = change("invoice", "invoice.payer_recorded", "in_9");
    expect(effectsOf(consoleSurface, c).invalidate).toEqual([consoleKeys.invoice(scope, "in_9")]);
    expect(effectsOf(portalSurface, c).invalidate).toEqual([portalKeys.invoice("in_9")]);
    expect(effectsOf(checkoutSurface, c).invalidate).toEqual([checkoutKeys.view("tok")]);
  });

  it("a payment, a checkout session and a credit note reach the invoices (they name no invoice)", () => {
    for (const entity of ["payment", "checkout_session", "credit_note"]) {
      const c = change(entity, `${entity}.x`);
      expect(effectsOf(consoleSurface, c).invalidate).toEqual([consoleKeys.invoices(scope)]);
      expect(effectsOf(portalSurface, c).invalidate).toEqual([portalKeys.invoices]);
      expect(effectsOf(checkoutSurface, c).invalidate).toEqual([checkoutKeys.view("tok")]);
    }
  });

  it("a subscription: the console's subscriptions and overview, the portal's subscriptions", () => {
    const c = change("subscription", "subscription.activated", "sub_1");
    expect(effectsOf(consoleSurface, c).invalidate).toEqual([consoleKeys.subscriptions(scope), ["console", ORG, "live", "overview"]]);
    expect(effectsOf(portalSurface, c).invalidate).toEqual([portalKeys.subscriptions]);
    expect(effectsOf(checkoutSurface, c).invalidate).toEqual([]);
  });

  it("a customer touches the console's customers only", () => {
    const c = change("customer", "customer.updated", "cus_1");
    expect(effectsOf(consoleSurface, c).invalidate).toEqual([consoleKeys.customers(scope)]);
    for (const s of [portalSurface, financeSurface, checkoutSurface]) expect(effectsOf(s, c).invalidate).toEqual([]);
  });

  it("a payout or a connection: the connections in the console, the connections and accounts in Finanças", () => {
    for (const entity of ["payout", "connection"]) {
      const c = change(entity, `${entity}.updated`);
      expect(effectsOf(consoleSurface, c).invalidate).toEqual([connectionKeys.list({org: ORG, mode: "live"})]);
      expect(effectsOf(financeSurface, c).invalidate).toEqual([
        connectionKeys.list({org: ORG, mode: "live"}), financeKeys.accounts("live", space),
      ]);
      expect(effectsOf(portalSurface, c).invalidate).toEqual([]);
    }
  });

  it("a personal Finanças space has no connection to refresh, only its accounts", () => {
    const personal: Surface = {kind: "finance", mode: "live", space: {kind: "personal"}};
    expect(effectsOf(personal, change("connection", "connection.updated")).invalidate).toEqual([
      financeKeys.accounts("live", {kind: "personal"}),
    ]);
  });

  it("a bank line or a bill refreshes the whole Finanças space and nothing of the console", () => {
    for (const entity of ["statement", "bill"]) {
      const c = change(entity, `${entity}.x`);
      expect(effectsOf(financeSurface, c).invalidate).toEqual([financeKeys.all("live", space)]);
      expect(effectsOf(consoleSurface, c).invalidate).toEqual([]);
    }
  });

  it("a type this client does not know, on an entity it does, still touches that entity's prefix", () => {
    const c = change("invoice", "invoice.something_new_from_a_newer_server");
    expect(effectsOf(portalSurface, c).invalidate).toEqual([portalKeys.invoices]);
    expect(effectsOf(consoleSurface, c).invalidate).toEqual([consoleKeys.invoices(scope), ["console", ORG, "live", "overview"]]);
  });

  it("an entity it does not know touches nothing (never everything)", () => {
    const c = change("hologram", "hologram.rendered");
    for (const s of [consoleSurface, portalSurface, financeSurface, checkoutSurface]) expect(effectsOf(s, c).invalidate).toEqual([]);
  });
});

describe("what a Gap invalidates", () => {
  it("is the surface's own root, and each surface's differs", () => {
    expect(rootsOf(consoleSurface)).toEqual([["console", ORG, "live"], connectionKeys.list({org: ORG, mode: "live"})]);
    expect(rootsOf(portalSurface)).toEqual([["portal"]]);
    expect(rootsOf(financeSurface)).toEqual([financeKeys.all("live", space), connectionKeys.list({org: ORG, mode: "live"})]);
    expect(rootsOf(checkoutSurface)).toEqual([checkoutKeys.view("tok")]);
  });
});
