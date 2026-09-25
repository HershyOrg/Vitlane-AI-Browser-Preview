// @vitest-environment jsdom

import { act, useEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CurrentUserProvider, useCurrentUser } from "../products/account/app/useCurrentUser";
import { LoginScreen } from "../products/account/iface/LoginScreen";
import { request } from "../shared/api/client";
import { LocaleProvider } from "../shared/i18n";
import { AuthBoundary } from "./AuthBoundary";

const user = {
  id: "user-1", email: "user@vitlane.test", displayName: "Vitlane User",
  createdAt: "2026-08-01T00:00:00Z", marketingAdmin: false, phase5Operator: false,
};
const workspacePath = "/curations/c-1?view=cart";
const loginPath = "/login?returnTo=%2Fcurations%2Fc-1%3Fview%3Dcart";

describe("AuthBoundary with the real authentication provider and login screen", () => {
  let root: Root | undefined;
  let container: HTMLDivElement;
  let auth: ReturnType<typeof useCurrentUser>;
  let paths: string[];

  afterEach(async () => {
    if (root) await act(async () => root?.unmount());
    container?.remove();
    root = undefined;
    vi.unstubAllGlobals();
    window.localStorage.clear();
  });

  function mockServer(authenticated = true, protectedStatus = 401) {
    const fetchMock = vi.fn(async (input: string | URL | Request) => {
      if (String(input) === "/api/v1/me" && authenticated) return Response.json({ user });
      if (String(input) === "/api/v1/auth/capabilities") {
        return Response.json({
          googleEnabled: true, localReviewEnabled: false, localReviewSeeded: false,
          localReviewProfiles: [], merchantEffectMode: "SANDBOX",
        });
      }
      return Response.json({ error: {
        code: protectedStatus === 403 ? "FRESH_AUTH_REQUIRED" : "AUTH_SESSION_EXPIRED",
        message: "Authentication required",
      } }, { status: protectedStatus });
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  async function render() {
    paths = [];
    function Observe() {
      const location = useLocation();
      auth = useCurrentUser();
      useEffect(() => { paths.push(`${location.pathname}${location.search}`); }, [location]);
      return null;
    }
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root?.render(
        <LocaleProvider>
          <CurrentUserProvider>
            <MemoryRouter initialEntries={[workspacePath]}>
              <Observe />
              <Routes>
                <Route element={<AuthBoundary />}>
                  <Route path="/curations/:id" element={<p>research workspace</p>} />
                </Route>
                <Route path="/login" element={<LoginScreen />} />
              </Routes>
            </MemoryRouter>
          </CurrentUserProvider>
        </LocaleProvider>,
      );
    });
  }

  it.each(["en-US", "ko-KR"])("%s: clears the expired user and reaches Google sign-in with the original return path", async (locale) => {
    document.cookie = `vt_locale_choice=${locale}; Path=/`;
    const fetchMock = mockServer();
    await render();
    expect(container.textContent).toContain("research workspace");

    await act(async () => {
      await Promise.all([1, 2, 3].map(() =>
        expect(request("/api/v1/account/overview")).rejects.toMatchObject({ status: 401 }),
      ));
    });
    expect(auth.user).toBeNull();
    expect(container.querySelectorAll("h1")).toHaveLength(1);
    expect(container.textContent).toContain(locale === "en-US" ? "Your session has expired" : "로그인 세션이 만료되었습니다");
    const label = locale === "en-US" ? "Sign in again" : "다시 로그인";
    const button = [...container.querySelectorAll("button")].find((item) => item.textContent === label);
    expect(button).toBeDefined();
    await act(async () => button?.click());

    expect(paths).toEqual([workspacePath, loginPath]);
    expect(container.querySelector('a[href="/api/v1/auth/google/start?returnTo=%2Fcurations%2Fc-1%3Fview%3Dcart"]')).not.toBeNull();
    expect(container.textContent).not.toContain("research workspace");
    expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/me")).toHaveLength(1);
    expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/account/overview")).toHaveLength(3);

    // A successful explicit session refresh after authentication resumes the route.
    await act(async () => { await auth.refresh(); });
    expect(auth.user).toEqual(user);
    expect(paths.at(-1)).toBe(workspacePath);
    expect(container.textContent).toContain("research workspace");
  });

  it("an unauthenticated initial /me probe goes directly to login without an expiry notice", async () => {
    mockServer(false);
    await render();
    expect(auth.user).toBeNull();
    expect(paths.at(-1)).toBe(loginPath);
    expect(container.textContent).not.toContain("로그인 세션이 만료되었습니다");
    expect(container.querySelector('a[href^="/api/v1/auth/google/start"]')).not.toBeNull();
  });

  it("operator fresh authentication does not expire the browser session", async () => {
    mockServer(true, 403);
    await render();
    await act(async () => {
      await expect(request("/api/v1/admin/ops/action")).rejects.toMatchObject({ code: "FRESH_AUTH_REQUIRED" });
    });
    expect(auth.user).toEqual(user);
    expect(container.textContent).toContain("research workspace");
    expect(paths).toEqual([workspacePath]);
  });

  it.each([200, 503])("a late /me response (%s) cannot overwrite session invalidation", async (status) => {
    const fetchMock = mockServer();
    await render();
    let resolveProbe!: (response: Response) => void;
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveProbe = resolve; }));
    let pendingRefresh!: ReturnType<typeof auth.refresh>;
    await act(async () => { pendingRefresh = auth.refresh(); });
    expect(auth.loading).toBe(true);
    await act(async () => {
      await expect(request("/api/v1/account/overview")).rejects.toMatchObject({ status: 401 });
    });
    expect(auth.user).toBeNull();
    expect(auth.loading).toBe(false);
    await act(async () => {
      resolveProbe(Response.json(status === 200 ? { user } : { error: { code: "INTERNAL_ERROR" } }, { status }));
      expect(await pendingRefresh).toBeNull();
    });
    expect(auth.user).toBeNull();
    expect(auth.error).toBeNull();
    expect(auth.loading).toBe(false);
    expect(container.textContent).toContain("로그인 세션이 만료되었습니다");
  });
});
