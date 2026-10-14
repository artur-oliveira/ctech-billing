import "@testing-library/jest-dom/vitest";

import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it} from "vitest";

import {OrganizationSwitch} from "@/components/console/OrganizationSwitch";
import type {MerchantOrganization} from "@/lib/api/consoleTypes";
import {getMerchantOrg} from "@/lib/console/merchant";

const A: MerchantOrganization = {organization_id: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", selector: "org:0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", display_name: "CTech Tecnologia", role: "owner", merchant_status: "live"};
const B: MerchantOrganization = {organization_id: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", selector: "org:0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb", display_name: "Padaria Bom Dia", role: "admin", merchant_status: "none"};

afterEach(() => window.localStorage.clear());

describe("OrganizationSwitch", () => {
  it("shows the selected organization by name, never its id", () => {
    render(<QueryClientProvider client={new QueryClient()}><OrganizationSwitch organizations={[A, B]} selected={A}/></QueryClientProvider>);
    expect(screen.getByRole("combobox", {name: "Organização"})).toHaveTextContent("CTech Tecnologia");
    expect(screen.queryByText(A.organization_id)).toBeNull();
  });

  it("stores the choice and removes the previous organization's queries", async () => {
    const client = new QueryClient();
    client.setQueryData(["console", A.organization_id, "live", "invoices"], {data: []});
    client.setQueryData(["console", "session"], {organizations: [A, B], organizations_unavailable: false});
    render(<QueryClientProvider client={client}><OrganizationSwitch organizations={[A, B]} selected={A}/></QueryClientProvider>);
    await userEvent.click(screen.getByRole("combobox", {name: "Organização"}));
    await userEvent.click(await screen.findByRole("option", {name: "Padaria Bom Dia"}));
    expect(getMerchantOrg()).toBe(B.organization_id);
    expect(client.getQueryData(["console", A.organization_id, "live", "invoices"])).toBeUndefined();
    expect(client.getQueryData(["console", "session"])).toBeDefined();
  });
});
