import {describe, expect, it} from "vitest";

import {currentFinanceSection} from "./nav";

describe("currentFinanceSection", () => {
  it("is Resumo only on the root", () => {
    expect(currentFinanceSection("/finance")).toBe("/finance");
  });

  it("is the section whose path the page is under", () => {
    expect(currentFinanceSection("/finance/bills")).toBe("/finance/bills");
    expect(currentFinanceSection("/finance/bills/bill_1")).toBe("/finance/bills");
    expect(currentFinanceSection("/finance/settings/connections/inter")).toBe("/finance/settings/connections");
  });

  it("puts the lines to classify under Contas, whose accounts they belong to", () => {
    expect(currentFinanceSection("/finance/unclassified")).toBe("/finance/accounts");
    expect(currentFinanceSection("/finance/unclassified?account=acc_1".split("?")[0])).toBe("/finance/accounts");
  });

  it("falls back to Resumo for a page no section owns", () => {
    expect(currentFinanceSection("/finance/spaces")).toBe("/finance");
  });
});
