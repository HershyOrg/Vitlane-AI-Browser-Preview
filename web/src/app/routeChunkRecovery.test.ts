import { describe, expect, it, vi } from "vitest";
import {
  attemptRouteChunkReload,
  clearRouteChunkReload,
  isRouteChunkLoadError,
  loadRouteModule,
  routeChunkRecoveryKey,
  type RouteChunkRecoveryEnvironment,
} from "./routeChunkRecovery";

function testEnvironment(): RouteChunkRecoveryEnvironment {
  const values = new Map<string, string>();
  return {
    pathname: "/curations/curation-1/order-sheet",
    search: "?cartVersion=3",
    storage: {
      getItem: (key) => values.get(key) ?? null,
      removeItem: (key) => { values.delete(key); },
      setItem: (key, value) => { values.set(key, value); },
    },
    reload: vi.fn(),
  };
}

describe("route chunk recovery", () => {
  it.each([
    "error loading dynamically imported module: /assets/OrderSheetPage-old.js",
    "Failed to fetch dynamically imported module: /assets/OrderSheetPage-old.js",
    "Importing a module script failed.",
    "ChunkLoadError: Loading chunk 42 failed.",
  ])("브라우저별 동적 청크 실패를 식별한다: %s", (message) => {
    expect(isRouteChunkLoadError(new TypeError(message))).toBe(true);
  });

  it("일반 화면/API 오류는 전체 페이지 reload 대상으로 보지 않는다", () => {
    expect(isRouteChunkLoadError(new Error("order sheet API returned 500"))).toBe(false);
  });

  it("같은 route와 URL에서는 자동 reload를 한 번만 수행한다", () => {
    const environment = testEnvironment();
    const error = new TypeError("error loading dynamically imported module");
    const key = routeChunkRecoveryKey("order-sheet", environment);

    expect(attemptRouteChunkReload("order-sheet", error, environment)).toBe(true);
    expect(environment.storage.getItem(key)).toBe("attempted");
    expect(environment.reload).toHaveBeenCalledTimes(1);
    expect(attemptRouteChunkReload("order-sheet", error, environment)).toBe(false);
    expect(environment.reload).toHaveBeenCalledTimes(1);

    clearRouteChunkReload("order-sheet", environment);
    expect(environment.storage.getItem(key)).toBeNull();
  });

  it("최신 route module을 읽으면 이전 reload marker를 제거한다", async () => {
    const environment = testEnvironment();
    environment.storage.setItem(routeChunkRecoveryKey("order-sheet", environment), "attempted");

    await expect(loadRouteModule("order-sheet", async () => ({ default: "loaded" }), environment))
      .resolves.toEqual({ default: "loaded" });
    expect(environment.storage.getItem(routeChunkRecoveryKey("order-sheet", environment))).toBeNull();
  });

  it("session storage가 막힌 환경에서는 reload loop 없이 오류 경계로 넘긴다", () => {
    const environment = testEnvironment();
    environment.storage.getItem = () => { throw new Error("storage denied"); };

    expect(attemptRouteChunkReload(
      "order-sheet",
      new TypeError("error loading dynamically imported module"),
      environment,
    )).toBe(false);
    expect(environment.reload).not.toHaveBeenCalled();
  });
});
