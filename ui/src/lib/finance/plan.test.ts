import {describe, expect, it} from "vitest";

import type {PlanPrice, PlanState} from "@/lib/api/financeTypes";
import {currentOption, overLimits, pendingOption, planOptions, quota, suggestBasic} from "./plan";

const P = (id: string, plan: string, amount: number, extra: Partial<PlanPrice> = {}, md: Record<string, string> = {}): PlanPrice => ({
  id, type: "fixed", unit_amount: amount, aggregation: "", meter: "", included_quantity: 0, metadata: {plan, ...md}, ...extra,
});
const PRICES: PlanPrice[] = [
  P("price_finance_pro_monthly", "pro", 4990, {}, {quota_spaces: "10", quota_people_per_space: "10"}),
  P("price_finance_free", "free", 0, {}, {quota_spaces: "1", quota_people_per_space: "1"}),
  P("price_finance_ondemand_spaces", "ondemand", 490, {type: "metered", aggregation: "max", meter: "finance_spaces", included_quantity: 1},
    {quota_spaces: "-1", quota_people_per_space: "-1"}),
  P("price_finance_ondemand_people", "ondemand", 290, {type: "metered", aggregation: "max", meter: "finance_people", included_quantity: 1},
    {quota_spaces: "-1", quota_people_per_space: "-1"}),
  P("price_finance_basic_monthly", "basic", 1990, {}, {quota_spaces: "3", quota_people_per_space: "5"}),
];
const state = (over: Partial<PlanState> = {}): PlanState => ({
  live_only: false, subscription: null, open_invoice: null, default_price_id: "price_finance_free", prices: PRICES,
  usage: {unavailable: false, spaces: []}, levels: {}, projected_ondemand_cents: 0, ...over,
});

describe("plan options", () => {
  it("orders Free, Basic, Pro, Sob demanda and groups the on-demand prices", () => {
    const o = planOptions(state());
    expect(o.map(x => x.key)).toEqual(["free", "basic", "pro", "ondemand"]);
    expect(o[0]).toMatchObject({isDefault: true, priceIds: [], quotaSpaces: 1, quotaPeople: 1});
    expect(o[3].priceIds.sort()).toEqual(["price_finance_ondemand_people", "price_finance_ondemand_spaces"]);
    expect(o[3].quotaSpaces).toBe(Infinity);
    expect(o[3].metered).toHaveLength(2);
  });

  it("reads a malformed quota as 0", () => {
    expect(quota("três")).toBe(0);
    expect(quota(undefined)).toBe(0);
    expect(quota("-1")).toBe(Infinity);
    expect(quota("3")).toBe(3);
  });

  it("is Free without a subscription and the subscription's plan with one", () => {
    const o = planOptions(state());
    expect(currentOption(state(), o).key).toBe("free");
    const s = state({subscription: {id: "s", status: "ACTIVE", cancel_at_period_end: false, price_ids: ["price_finance_basic_monthly"], period: {period_start: "2026-10-01", period_end: "2026-11-01"}, pending_change: null}});
    expect(currentOption(s, o).key).toBe("basic");
  });
});

describe("over the limit", () => {
  const spaces = (n: number, people = 0) => Array.from({length: n}, (_, i) => ({id: `w${i}`, display_name: `E${i}`, people, pending_invitations: 0}));

  it("names spaces over the plan's allowance", () => {
    const s = state({usage: {unavailable: false, spaces: spaces(6)}});
    const basic = planOptions(s)[1];
    expect(overLimits(s, basic)).toEqual([{resource: "spaces", used: 6, limit: 3}]);
  });

  it("counts pending invitations as people, per space", () => {
    const s = state({usage: {unavailable: false, spaces: [{id: "w", display_name: "Casa", people: 4, pending_invitations: 2}]}});
    expect(overLimits(s, planOptions(s)[1])).toEqual([{resource: "people", used: 6, limit: 5, spaceName: "Casa"}]);
  });

  it("says nothing on unknown usage", () => {
    const s = state({usage: {unavailable: true, spaces: spaces(9)}});
    expect(overLimits(s, planOptions(s)[0])).toEqual([]);
  });

  it("never limits Sob demanda", () => {
    const s = state({usage: {unavailable: false, spaces: spaces(30, 30)}});
    expect(overLimits(s, planOptions(s)[3])).toEqual([]);
  });
});

describe("a scheduled change", () => {
  it("names the plan and the day it starts", () => {
    const s = state({subscription: {id: "s", status: "ACTIVE", cancel_at_period_end: false, price_ids: ["price_finance_basic_monthly"],
      period: {period_start: "2026-10-01", period_end: "2026-11-01"},
      pending_change: {price_ids: ["price_finance_ondemand_spaces", "price_finance_ondemand_people"], at: "2026-11-01"}}});
    const o = planOptions(s);
    expect(currentOption(s, o).key).toBe("basic"); // the limits hold until then
    expect(pendingOption(s, o)).toEqual({option: o[3], at: "2026-11-01"});
  });
});

describe("Sob demanda above Basic", () => {
  const onDemand = {id: "s", status: "ACTIVE", cancel_at_period_end: false,
    price_ids: ["price_finance_ondemand_spaces", "price_finance_ondemand_people"], period: {period_start: "2026-10-01", period_end: "2026-11-01"}, pending_change: null};
  it("suggests Basic only when the projection passes it", () => {
    const over = state({subscription: onDemand, projected_ondemand_cents: 2830});
    const o = planOptions(over);
    expect(suggestBasic(over, o, currentOption(over, o))?.key).toBe("basic");
    const under = state({subscription: onDemand, projected_ondemand_cents: 1990});
    expect(suggestBasic(under, o, currentOption(under, o))).toBeNull();
    expect(suggestBasic(state({projected_ondemand_cents: 9999}), o, o[0])).toBeNull();
  });
});
