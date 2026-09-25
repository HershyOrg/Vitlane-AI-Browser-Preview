// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { setAnalyticsIdentity } from "../../../shared/analytics/analytics";
import { getCurrentUser, logout } from "../infra/accountApi";
import { CurrentUserProvider, useCurrentUser } from "./useCurrentUser";

vi.mock("../infra/accountApi", () => ({ getCurrentUser: vi.fn(), logout: vi.fn() }));
vi.mock("../../../shared/analytics/analytics", () => ({ setAnalyticsIdentity: vi.fn(), clearAnalyticsIdentity: vi.fn() }));
const user = { id: "alice", email: "alice@example.test", displayName: "Alice", createdAt: "2026-09-24T00:00:00Z", marketingAdmin: false, phase5Operator: false };
let state: ReturnType<typeof useCurrentUser>;
let root: Root, container: HTMLDivElement;
const loadingStates: boolean[] = [];
function Probe() { state = useCurrentUser(); loadingStates.push(state.loading); return null; }
beforeEach(async () => {
  vi.resetAllMocks(); loadingStates.length = 0;
  vi.mocked(getCurrentUser).mockResolvedValue({ user });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => { root.render(<LocaleProvider><CurrentUserProvider><Probe /></CurrentUserProvider></LocaleProvider>); });
  loadingStates.length = 0;
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); });
it("optional identity refresh leaves the workspace mounted on success and failure", async () => {
  vi.mocked(getCurrentUser).mockResolvedValueOnce({ user: { ...user, analyticsUserId: "pseudonym" } });
  await act(async () => state.refreshAnalyticsIdentity());
  expect(state.user?.analyticsUserId).toBe("pseudonym");
  vi.mocked(getCurrentUser).mockRejectedValueOnce(new Error("network unavailable"));
  await act(async () => state.refreshAnalyticsIdentity());
  expect(state.user?.id).toBe("alice");
  expect(state.error).toBeNull();
  expect(loadingStates).not.toContain(true);
});
it("an identity response arriving after logout cannot restore an account or analytics identity", async () => {
  let resolve!: (value: { user: typeof user }) => void;
  vi.mocked(getCurrentUser).mockImplementationOnce(() => new Promise(complete => { resolve = complete; }));
  const pending = state.refreshAnalyticsIdentity();
  vi.mocked(logout).mockResolvedValue();
  await act(async () => state.logout());
  vi.mocked(setAnalyticsIdentity).mockClear();
  await act(async () => { resolve({ user }); await pending; });
  expect(state.user).toBeNull();
  expect(setAnalyticsIdentity).not.toHaveBeenCalled();
});
