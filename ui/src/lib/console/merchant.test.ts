import {afterEach, describe, expect, it} from "vitest";

import type {MerchantOrganization} from "@/lib/api/consoleTypes";
import {getMerchantOrg, pickMerchantOrg, setMerchantOrg, tenantInMode} from "@/lib/console/merchant";

const A = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
const B = "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb";
const org = (id: string, merchant_status: MerchantOrganization["merchant_status"]): MerchantOrganization =>
  ({organization_id: id, selector: `org:${id}`, display_name: id, role: "owner", merchant_status});

afterEach(() => window.localStorage.clear());

describe("tenantInMode", () => {
  it.each([
    ["none", "live", false], ["none", "test", false],
    ["test", "live", false], ["test", "test", true],
    ["live", "live", true], ["live", "test", true],
    ["rejected", "live", false], ["rejected", "test", true],
    ["suspended", "live", true], ["suspended", "test", true],
  ] as const)("%s in %s is %s", (status, mode, want) => {
    expect(tenantInMode(status, mode)).toBe(want);
  });
});

describe("pickMerchantOrg", () => {
  it("keeps the stored organization while the person still administers it", () => {
    expect(pickMerchantOrg([org(A, "live"), org(B, "none")], B, "live")?.organization_id).toBe(B);
  });
  it("falls back to the first organization with a tenant in this mode", () => {
    expect(pickMerchantOrg([org(A, "none"), org(B, "test")], "gone", "test")?.organization_id).toBe(B);
  });
  it("falls back to the first organization when none has a tenant", () => {
    expect(pickMerchantOrg([org(A, "none"), org(B, "none")], null, "live")?.organization_id).toBe(A);
  });
  it("is null with no organizations", () => {
    expect(pickMerchantOrg([], A, "live")).toBeNull();
  });
});

describe("the stored organization", () => {
  it("round-trips a canonical id and refuses anything else", () => {
    setMerchantOrg(A);
    expect(getMerchantOrg()).toBe(A);
    window.localStorage.setItem("ctech-billing-console-organization", "USER#bob");
    expect(getMerchantOrg()).toBeNull();
  });
});
