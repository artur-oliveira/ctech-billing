import {beforeEach, describe, expect, it, vi} from "vitest";

import {apiClient} from "@/lib/api/client";
import {connect, getConnection, listConnections} from "@/lib/api/connections";

const scope = {org: "01a04ed6-1af9-745e-bcf7-d3b66fe52321", mode: "live" as const};

describe("connections api", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("sends the organization and the mode on every call", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({data: {data: [], providers: []}});
    await listConnections(scope);
    expect(get).toHaveBeenCalledWith("/v1.0/console/connections",
      expect.objectContaining({headers: expect.objectContaining({"X-Billing-Space": `org:${scope.org}`, "X-Billing-Mode": "live"})}));
  });

  it("reads a never-connected provider as null, not as an error", async () => {
    vi.spyOn(apiClient, "get").mockRejectedValue({isAxiosError: true, response: {status: 404, data: {code: "resource_not_found"}}});
    expect(await getConnection(scope, "inter")).toBeNull();
  });

  it("uploads the pair as multipart with the idempotency key, and test mode sends an empty object", async () => {
    const put = vi.spyOn(apiClient, "put").mockResolvedValue({data: {provider: "inter"}});
    const cert = new File(["c"], "inter.crt"), key = new File(["k"], "inter.key");
    await connect(scope, "inter", {client_id: "id", client_secret: "s", account: "1234567", pix_key: "evp", notify_email: "a@b.co", backfill_months: 3, authorized: true, cert, key}, "idem-1");
    const [, body, cfg] = put.mock.calls[0];
    expect(body).toBeInstanceOf(FormData);
    expect((body as FormData).get("cert")).toBe(cert);
    expect((body as FormData).get("authorized")).toBe("true");
    expect(cfg?.headers?.["Idempotency-Key"]).toBe("idem-1");
    await connect({...scope, mode: "test"}, "inter", null, "idem-2");
    expect(put.mock.calls[1][1]).toEqual({});
  });
});
