// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getOpsHealth,
  getOpsHeartbeats,
  getOpsSamples,
} from "./opsApi";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("ops API contract", () => {
  it("503 core unavailable도 오류 envelope가 아닌 운영 readback으로 읽는다", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      status: "unavailable",
      core: {
        status: "unavailable",
        reasonCodes: ["DATABASE_UNAVAILABLE"],
        databasePool: {},
      },
      degradedReasonCodes: [],
      http: {},
    }), {
      status: 503,
      headers: { "content-type": "application/json" },
    })));

    const health = await getOpsHealth();

    expect(health.status).toBe("unavailable");
    expect(health.core.reasonCodes).toEqual(["DATABASE_UNAVAILABLE"]);
  });

  it("L1 heartbeat와 bounded sample 기간을 별도 readback으로 요청한다", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        configured: false,
        checks: [],
      }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        hours: 168,
        points: [],
      }), {
        status: 200,
        headers: { "content-type": "application/json" },
      })));

    await getOpsHeartbeats();
    await getOpsSamples(168);

    expect(fetch).toHaveBeenNthCalledWith(
      1,
      "/api/v1/admin/ops/heartbeats",
      expect.objectContaining({ credentials: "same-origin" }),
    );
    expect(fetch).toHaveBeenNthCalledWith(
      2,
      "/api/v1/admin/ops/samples?hours=168",
      expect.objectContaining({ credentials: "same-origin" }),
    );
  });
});
