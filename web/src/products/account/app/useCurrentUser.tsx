import {
  createContext,
  ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { setAnalyticsIdentity, clearAnalyticsIdentity } from "../../../shared/analytics/analytics";
import { APIError, authSessionExpiredEvent } from "../../../shared/api/client";
import type { CurrentUser } from "../../../shared/api/types";
import { useLocale } from "../../../shared/i18n";
import {
  getCurrentUser,
  logout as requestLogout,
} from "../infra/accountApi";

type AuthState = {
  user: CurrentUser | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<CurrentUser | null>;
  refreshAnalyticsIdentity: () => Promise<void>;
  logout: () => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

export function CurrentUserProvider({ children }: { children: ReactNode }) {
  const { l } = useLocale();
  const [user, setUser] = useState<CurrentUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const refreshVersion = useRef(0);

  useEffect(() => {
    const expireSession = () => {
      // A /me response started before expiry must not restore the old user.
      refreshVersion.current += 1;
      clearAnalyticsIdentity();
      setUser(null);
      setError(null);
      setLoading(false);
    };
    window.addEventListener(authSessionExpiredEvent, expireSession);
    return () => {
      refreshVersion.current += 1;
      window.removeEventListener(authSessionExpiredEvent, expireSession);
    };
  }, []);

  const refresh = useCallback(async () => {
    const version = ++refreshVersion.current;
    setLoading(true);
    setError(null);
    try {
      const result = await getCurrentUser();
      if (version !== refreshVersion.current) return null;
      setAnalyticsIdentity(result.user);
      setUser(result.user);
      return result.user;
    } catch (caught) {
      if (version !== refreshVersion.current) return null;
      clearAnalyticsIdentity();
      setUser(null);
      if (!(caught instanceof APIError && caught.status === 401)) {
        setError(l(
          "We couldn't verify your sign-in status. Check the server connection.",
          "로그인 상태를 확인하지 못했습니다. 서버 연결을 확인해 주세요.",
        ));
      }
      return null;
    } finally {
      if (version === refreshVersion.current) setLoading(false);
    }
  }, [l]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Optional telemetry must not remount AuthBoundary or discard an unsaved draft.
  const refreshAnalyticsIdentity = useCallback(async () => {
    const version = refreshVersion.current;
    const expectedUserID = user?.id;
    if (!expectedUserID) return;
    try {
      const result = await getCurrentUser();
      if (version !== refreshVersion.current || result.user.id !== expectedUserID) return;
      setAnalyticsIdentity(result.user);
      setUser(current => current?.id === expectedUserID
        ? { ...current, analyticsUserId: result.user.analyticsUserId }
        : current);
    } catch { /* Optional analytics failure leaves the product session alone. */ }
  }, [user?.id]);

  const logout = useCallback(async () => {
    await requestLogout();
    refreshVersion.current += 1;
    clearAnalyticsIdentity();
    setUser(null);
  }, []);

  const value = useMemo(
    () => ({ user, loading, error, refresh, refreshAnalyticsIdentity, logout }),
    [user, loading, error, refresh, refreshAnalyticsIdentity, logout],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useCurrentUser(): AuthState {
  const value = useContext(AuthContext);
  if (!value) {
    throw new Error("useCurrentUser must be used inside CurrentUserProvider");
  }
  return value;
}
