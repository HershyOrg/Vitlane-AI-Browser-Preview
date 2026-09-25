// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError, request } from "../../../shared/api/client";
import { OperatorFreshAuthAlert } from "./OperatorFreshAuthAlert";

describe("OperatorFreshAuthAlert", () => {
  let container: HTMLDivElement | undefined;

  afterEach(() => {
    container?.remove();
    container = undefined;
    vi.unstubAllGlobals();
  });

  it("모든 FRESH_AUTH_REQUIRED 응답을 현재 화면 복귀 전역 Alert로 연결한다", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 403,
      json: async () => ({
        error: {
          code: "FRESH_AUTH_REQUIRED",
          message: "최근 인증이 필요합니다.",
        },
      }),
    } as Response));

    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => {
      root.render(
	    <MemoryRouter initialEntries={["/admin/agencyOrder"]}>
          <OperatorFreshAuthAlert />
        </MemoryRouter>,
      );
    });

    await act(async () => {
      await expect(request("/api/v1/admin/example", {
        method: "POST",
        body: "{}",
      })).rejects.toEqual(expect.objectContaining<Partial<APIError>>({
        code: "FRESH_AUTH_REQUIRED",
        status: 403,
      }));
    });

    expect(document.body.textContent).toContain("운영자 권한 재인증");
    expect(document.body.textContent).toContain("다시 인증해야 합니다");
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    expect(document.querySelector<HTMLAnchorElement>(
      'a[href="/api/v1/auth/google/start?fresh=1&returnTo=%2Fadmin%2FagencyOrder"]',
    )).not.toBeNull();

    await act(async () => root.unmount());
  });
});
