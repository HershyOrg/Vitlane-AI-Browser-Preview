// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RouteLoadBoundary } from "./RouteLoadBoundary";

function BrokenRoute(): never {
  throw new Error("route render failed");
}

describe("RouteLoadBoundary", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.restoreAllMocks();
  });

  it("route render 실패를 흰 화면 대신 복구 안내로 표시한다", async () => {
    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/curations/curation-1/order-sheet?cartVersion=3"]}>
          <RouteLoadBoundary><BrokenRoute /></RouteLoadBoundary>
        </MemoryRouter>,
      );
    });

    expect(container.textContent).toContain("이 화면을 열지 못했습니다");
    expect(container.textContent).toContain("장바구니는 그대로 저장되어 있습니다");
    expect(container.querySelector("button")?.textContent).toContain("페이지 새로고침");
  });
});
