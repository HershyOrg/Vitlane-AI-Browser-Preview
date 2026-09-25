// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  APIError,
  authSessionExpiredEvent,
  operatorFreshAuthRequiredEvent,
  request,
} from "./client";

describe("API authentication events", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("보호 API의 401을 세션 만료 이벤트로 알린다", async () => {
    vi.stubGlobal("fetch", unauthorized("UNAUTHORIZED"));
    const listener = vi.fn();
    window.addEventListener(authSessionExpiredEvent, listener);

    await expect(request("/api/v1/curations/c-1")).rejects.toBeInstanceOf(APIError);

    expect(listener).toHaveBeenCalledTimes(1);
    window.removeEventListener(authSessionExpiredEvent, listener);
  });

  it("최초 로그인 확인과 인증 API의 401은 세션 만료로 오인하지 않는다", async () => {
    vi.stubGlobal("fetch", unauthorized("UNAUTHORIZED"));
    const listener = vi.fn();
    window.addEventListener(authSessionExpiredEvent, listener);

    await expect(request("/api/v1/me")).rejects.toBeInstanceOf(APIError);
    await expect(request("/api/v1/auth/logout", { method: "POST" })).rejects.toBeInstanceOf(APIError);

    expect(listener).not.toHaveBeenCalled();
    window.removeEventListener(authSessionExpiredEvent, listener);
  });

  it("운영자 재인증 요구는 일반 세션 만료와 분리한다", async () => {
    vi.stubGlobal("fetch", unauthorized("FRESH_AUTH_REQUIRED"));
    const expired = vi.fn();
    const fresh = vi.fn();
    window.addEventListener(authSessionExpiredEvent, expired);
    window.addEventListener(operatorFreshAuthRequiredEvent, fresh);

    await expect(request("/api/v1/admin/example")).rejects.toBeInstanceOf(APIError);

    expect(fresh).toHaveBeenCalledTimes(1);
    expect(expired).not.toHaveBeenCalled();
    window.removeEventListener(authSessionExpiredEvent, expired);
    window.removeEventListener(operatorFreshAuthRequiredEvent, fresh);
  });
});

function unauthorized(code: string) {
  return vi.fn().mockResolvedValue({
    ok: false,
    status: 401,
    json: async () => ({ error: { code, message: "unauthorized" } }),
  } as Response);
}
