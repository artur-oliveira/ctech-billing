import {describe, expect, it} from "vitest";

import {t} from "@/lib/i18n";

import {consoleNav} from "./nav";

// Finanças is its own area now (/finance): the console is invoicing only.
describe("consoleNav", () => {
  it("is empty until the session says there is an organization", () => {
    expect(consoleNav(false)).toEqual([]);
  });
  it("lists only the invoicing sections once it does", () => {
    const labels = consoleNav(true).map(i => t(i.label));
    expect(labels[0]).toBe("Visão geral");
    expect(labels).not.toContain("Finanças");
  });
  it("adds Solicitações only for tenant zero's organization", () => {
    expect(consoleNav(true, true).map(i => i.href)).toContain("/console/applications");
    expect(consoleNav(true, false).map(i => i.href)).not.toContain("/console/applications");
    expect(consoleNav(false, true)).toEqual([]);
  });
  it("lists the bank connections (BYOK) with the invoicing sections", () => {
    expect(consoleNav(true).map(i => i.href)).toContain("/console/connections");
  });
  it("lists the BYOK plan after the bank connections", () => {
    const hrefs = consoleNav(true).map(i => i.href);
    expect(hrefs.indexOf("/console/plan")).toBe(hrefs.indexOf("/console/connections") + 1);
  });
  it("lists the integration tools before the settings", () => {
    const labels = consoleNav(true).map(i => t(i.label));
    expect(labels.slice(-3)).toEqual(["Credenciais", "Webhooks", "Configurações"]);
  });
});
