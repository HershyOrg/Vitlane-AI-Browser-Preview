// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { request } from "../../../shared/api/client";
import type { CurationListPage, SidebarCuration } from "../infra/curationApi";
import {
  publishCurationCreated,
  publishCurationCreationUnconfirmed,
  useSidebarCurations,
  withCreatedCuration,
  withNewerCurations,
  withOlderCurations,
  type SidebarCurations,
} from "./useSidebarCurations";

vi.mock("../../../shared/api/client", () => ({ request: vi.fn() }));
const api = vi.mocked(request);

const row = (id: string): SidebarCuration => ({
  curationId: id,
  intentSummary: `제목 ${id}`,
  createdAt: "2026-09-16T09:00:00Z",
});
const page = (ids: string[], cursors: Partial<CurationListPage> = {}): CurationListPage => ({
  schemaVersion: "vitlane.curation-list.v2",
  curations: ids.map(row),
  ...cursors,
});
const ids = (curations: SidebarCuration[]) => curations.map(({ curationId }) => curationId);

describe("사이드바 목록 병합", () => {
  const list = { curations: [row("c"), row("b"), row("a")], nextCursor: "older", latestCursor: "latest-c" };

  it("새로 생긴 것은 위에 붙이고 겹치는 행은 한 번만 남긴다", () => {
    const merged = withNewerCurations(list, page(["e", "d", "c"], { latestCursor: "latest-e" }));
    expect(ids(merged.curations)).toEqual(["e", "d", "c", "b", "a"]);
    expect(merged.nextCursor).toBe("older");
    expect(merged.latestCursor).toBe("latest-e");
  });

  it("새 것이 없으면 목록을 그대로 둔다", () => {
    expect(withNewerCurations(list, page([], { latestCursor: "latest-c" }))).toBe(list);
  });

  it("새 것이 한 쪽보다 많으면 그 쪽으로 새로 시작한다", () => {
    const restarted = withNewerCurations(list, page(["z", "y"], { nextCursor: "after-y", latestCursor: "latest-z" }));
    expect(ids(restarted.curations)).toEqual(["z", "y"]);
    expect(restarted.nextCursor).toBe("after-y");
  });

  it("더 보기는 아래에 붙이고 다음 커서를 바꾼다", () => {
    const extended = withOlderCurations(list, page(["a", "9"], {}));
    expect(ids(extended.curations)).toEqual(["c", "b", "a", "9"]);
    expect(extended.nextCursor).toBeUndefined();
    expect(extended.latestCursor).toBe("latest-c");
  });

  it("이 탭에서 만든 큐레이션은 맨 위에 한 번만 둔다", () => {
    expect(ids(withCreatedCuration(list, row("b")).curations)).toEqual(["b", "c", "a"]);
    expect(ids(withCreatedCuration(list, row("n")).curations)).toEqual(["n", "c", "b", "a"]);
  });
});

describe("useSidebarCurations", () => {
  let root: Root;
  let latest: SidebarCurations;
  let visibility: DocumentVisibilityState;

  function Probe() {
    latest = useSidebarCurations();
    return null;
  }

  beforeEach(() => {
    api.mockReset();
    visibility = "visible";
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => visibility });
  });
  afterEach(async () => {
    await act(async () => root?.unmount());
    document.body.innerHTML = "";
  });

  async function mount() {
    const element = document.createElement("div");
    document.body.append(element);
    root = createRoot(element);
    await act(async () => root.render(<Probe />));
  }
  const paths = () => api.mock.calls.map(([path]) => path);

  it("처음 한 번 읽고, 탭이 다시 보이면 그 이후 것만 묻는다", async () => {
    api.mockResolvedValueOnce(page(["b", "a"], { latestCursor: "L1" }));
    await mount();
    expect(paths()).toEqual(["/api/v1/curations"]);
    expect(ids(latest.curations)).toEqual(["b", "a"]);
    expect(latest.loading).toBe(false);

    // Switching back fires both focus and visibilitychange; one small request answers both.
    let answer!: (value: CurationListPage) => void;
    api.mockImplementationOnce(() => new Promise((resolve) => { answer = resolve as typeof answer; }));
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(paths()).toEqual(["/api/v1/curations", "/api/v1/curations?after=L1"]);
    await act(async () => answer(page(["c"], { latestCursor: "L2" })));
    expect(ids(latest.curations)).toEqual(["c", "b", "a"]);

    // A hidden tab does not ask.
    visibility = "hidden";
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    expect(api).toHaveBeenCalledTimes(2);

    // The next check starts after the newest row the tab now knows.
    visibility = "visible";
    api.mockResolvedValueOnce(page([], { latestCursor: "L2" }));
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    expect(paths()[2]).toBe("/api/v1/curations?after=L2");
    expect(ids(latest.curations)).toEqual(["c", "b", "a"]);
  });

  it("이 탭에서 만든 큐레이션은 요청 없이 보인다", async () => {
    api.mockResolvedValueOnce(page(["a"], { latestCursor: "L1" }));
    await mount();
    await act(async () => publishCurationCreated(row("new")));
    expect(ids(latest.curations)).toEqual(["new", "a"]);
    expect(api).toHaveBeenCalledTimes(1);
  });

  it("결과를 모르는 생성 실패 뒤에는 새 것을 한 번 묻는다", async () => {
    api.mockResolvedValueOnce(page(["a"], { latestCursor: "L1" }));
    await mount();
    api.mockResolvedValueOnce(page(["saved"], { latestCursor: "L2" }));
    await act(async () => publishCurationCreationUnconfirmed());
    expect(paths()).toEqual(["/api/v1/curations", "/api/v1/curations?after=L1"]);
    expect(ids(latest.curations)).toEqual(["saved", "a"]);
  });

  it("이미 나간 확인이 있으면 그 답을 받은 뒤 한 번 더 묻는다", async () => {
    api.mockResolvedValueOnce(page(["a"], { latestCursor: "L1" }));
    await mount();
    let answer!: (value: CurationListPage) => void;
    api.mockImplementationOnce(() => new Promise((resolve) => { answer = resolve as typeof answer; }));
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    // That check may have left before the lost creation was saved.
    await act(async () => publishCurationCreationUnconfirmed());
    expect(api).toHaveBeenCalledTimes(2);
    api.mockResolvedValueOnce(page(["saved"], { latestCursor: "L2" }));
    await act(async () => answer(page([], { latestCursor: "L1" })));
    expect(paths()).toEqual([
      "/api/v1/curations",
      "/api/v1/curations?after=L1",
      "/api/v1/curations?after=L1",
    ]);
    expect(ids(latest.curations)).toEqual(["saved", "a"]);
  });

  it("빈 목록은 물을 기준 행이 없으니 첫 쪽을 다시 읽는다", async () => {
    api.mockResolvedValueOnce(page([]));
    await mount();
    expect(latest.curations).toEqual([]);
    api.mockResolvedValueOnce(page([]));
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    // A first Curation whose creation ended unconfirmed still shows up.
    api.mockResolvedValueOnce(page(["first"], { latestCursor: "L1" }));
    await act(async () => publishCurationCreationUnconfirmed());
    expect(paths()).toEqual(["/api/v1/curations", "/api/v1/curations", "/api/v1/curations"]);
    expect(ids(latest.curations)).toEqual(["first"]);
    expect(latest.failed).toBe(false);
    expect(latest.loading).toBe(false);
  });

  it("다시 읽기가 실패해도 가진 목록을 지우지 않는다", async () => {
    api.mockResolvedValueOnce(page(["a"], { latestCursor: "L1" }));
    await mount();
    api.mockRejectedValueOnce(new Error("offline"));
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    expect(ids(latest.curations)).toEqual(["a"]);
    expect(latest.failed).toBe(false);
    expect(latest.loading).toBe(false);
  });

  it("읽을 수 없는 응답은 실패로 다뤄 셸을 무너뜨리지 않는다", async () => {
    api.mockResolvedValueOnce({ schemaVersion: "vitlane.curation-list.v2" } as never);
    await mount();
    expect(latest.failed).toBe(true);
    expect(latest.curations).toEqual([]);
  });

  it("첫 읽기가 실패하면 알리고, 탭 복귀를 재시도로 쓴다", async () => {
    api.mockRejectedValueOnce(new Error("offline"));
    await mount();
    expect(latest.failed).toBe(true);
    api.mockResolvedValueOnce(page(["a"], { latestCursor: "L1" }));
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    expect(paths()).toEqual(["/api/v1/curations", "/api/v1/curations"]);
    expect(latest.failed).toBe(false);
    expect(ids(latest.curations)).toEqual(["a"]);
  });

  it("더 보기는 before로 이어 읽고 끝나면 멈춘다", async () => {
    api.mockResolvedValueOnce(page(["d", "c"], { nextCursor: "N1", latestCursor: "L1" }));
    await mount();
    expect(latest.hasMore).toBe(true);
    api.mockResolvedValueOnce(page(["b", "a"], {}));
    await act(async () => latest.loadMore());
    expect(paths()[1]).toBe("/api/v1/curations?before=N1");
    expect(ids(latest.curations)).toEqual(["d", "c", "b", "a"]);
    expect(latest.hasMore).toBe(false);
    await act(async () => latest.loadMore());
    expect(api).toHaveBeenCalledTimes(2);
  });

  it("목록이 새로 시작되면 늦게 온 이전 쪽을 붙이지 않는다", async () => {
    api.mockResolvedValueOnce(page(["d", "c"], { nextCursor: "N1", latestCursor: "L1" }));
    await mount();
    let olderAnswer!: (value: CurationListPage) => void;
    api.mockImplementationOnce(() => new Promise((resolve) => { olderAnswer = resolve as typeof olderAnswer; }));
    await act(async () => latest.loadMore());
    // Meanwhile more new Curations than a page arrive, and the list starts over.
    api.mockResolvedValueOnce(page(["z", "y"], { nextCursor: "N2", latestCursor: "L2" }));
    await act(async () => document.dispatchEvent(new Event("visibilitychange")));
    expect(ids(latest.curations)).toEqual(["z", "y"]);
    await act(async () => olderAnswer(page(["b", "a"], {})));
    expect(ids(latest.curations)).toEqual(["z", "y"]);
    expect(latest.hasMore).toBe(true);
  });
});
