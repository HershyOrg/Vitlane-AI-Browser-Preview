// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import type { ExpansionResult } from "../../../shared/api/types";
import { createExpansion, getCurrentExpansion } from "./curationApi";

const expansion: ExpansionResult = {
  run: {
    id: "run-1",
    curationId: "curation-1",
    planId: "plan-1",
    userId: "user-1",
    kind: "EXPANSION",
    instruction: "휴대용 모니터도 추가해줘",
    planningTaskId: "planning-task-1",
    status: "REQUESTED",
    createdAt: "2026-07-28T00:00:00Z",
    updatedAt: "2026-07-28T00:00:00Z",
  },
  planningTask: {
    id: "planning-task-1",
    planId: "plan-1",
    userId: "user-1",
    contextVersion: 1,
    contextHash: "context-hash",
    status: "REQUESTED",
    expiresAt: "2026-07-28T01:00:00Z",
    createdAt: "2026-07-28T00:00:00Z",
    updatedAt: "2026-07-28T00:00:00Z",
  },
  intelligenceJob: { jobId: "job-expansion", replay: false },
  replay: false,
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

describe("Curation expansion API", () => {
  it("전송 실패 뒤 같은 요청을 재시도하면 동일한 idempotency key를 재사용한다", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError("network unavailable"))
      .mockResolvedValueOnce(
        new Response(JSON.stringify(expansion), {
          status: 202,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "11111111-1111-4111-8111-111111111111",
    );

    await expect(
      createExpansion("plan-1", "휴대용 모니터도 추가해줘"),
    ).rejects.toThrow("network unavailable");
    await expect(
      createExpansion("plan-1", "휴대용 모니터도 추가해줘"),
    ).resolves.toEqual(expansion);

    expect(fetchMock).toHaveBeenCalledTimes(2);
    for (const [path, init] of fetchMock.mock.calls) {
      expect(path).toBe("/api/v1/shopping-plans/plan-1/expansions");
      expect(init?.method).toBe("POST");
      expect(init?.body).toBe(
        JSON.stringify({ instruction: "휴대용 모니터도 추가해줘" }),
      );
      expect(init?.headers).toMatchObject({
        "Idempotency-Key": "11111111-1111-4111-8111-111111111111",
      });
    }
    expect(
      sessionStorage.getItem("vitlane.curation.expansion.retry.plan-1"),
    ).toBeNull();
  });

  it("새로고침 복구용 현재 expansion을 plan 범위에서 조회한다", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      new Response(JSON.stringify(expansion), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(getCurrentExpansion("plan-1")).resolves.toEqual(expansion);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/shopping-plans/plan-1/expansions/current",
      expect.objectContaining({
        credentials: "same-origin",
      }),
    );
  });
});
