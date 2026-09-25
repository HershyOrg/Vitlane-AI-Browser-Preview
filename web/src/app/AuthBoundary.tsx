import { useEffect, useState } from "react";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router";
import { useCurrentUser } from "../products/account/app/useCurrentUser";
import { authSessionExpiredEvent } from "../shared/api/client";
import { useLocale } from "../shared/i18n";
import { BrandMark, FeedbackState } from "../shared/ui";

export function AuthBoundary() {
  const { l } = useLocale();
  const { user, loading, error } = useCurrentUser();
  const location = useLocation();
  const navigate = useNavigate();
  const [sessionExpired, setSessionExpired] = useState(false);

  useEffect(() => {
    const expireSession = () => setSessionExpired(true);
    window.addEventListener(authSessionExpiredEvent, expireSession);
    return () => window.removeEventListener(authSessionExpiredEvent, expireSession);
  }, []);

  if (loading) {
    return (
      <main className="catalog-ui-auth-state">
        <BrandMark href="/" />
        <h1>{l("Checking your sign-in status", "로그인 상태를 확인하고 있습니다")}</h1>
        <FeedbackState
          state="loading"
          description={l(
            "Checking the saved session and your intended destination.",
            "서버에 저장된 로그인과 원래 이동할 화면을 확인합니다.",
          )}
        />
      </main>
    );
  }
  if (error) {
    return (
      <main className="catalog-ui-auth-state">
        <BrandMark href="/" />
        <h1>{l("We couldn't verify your sign-in status", "로그인 상태를 확인하지 못했습니다")}</h1>
        <FeedbackState
          state="error"
          description={error}
          action={{
            label: l("Go to sign in", "로그인으로 이동"),
            onAction: () => {
              window.location.href = "/login";
            },
          }}
        />
      </main>
    );
  }
  if (sessionExpired) {
    const returnTo = `${location.pathname}${location.search}`;
    return (
      <main className="catalog-ui-auth-state">
        <BrandMark href="/" />
        <h1>{l("Your session has expired", "로그인 세션이 만료되었습니다")}</h1>
        <FeedbackState
          state="error"
          description={l(
            "Sign in again to continue from this page. Your saved work is unchanged.",
            "이 화면에서 계속하려면 다시 로그인해 주세요. 저장된 내용은 그대로 유지됩니다.",
          )}
          action={{
            label: l("Sign in again", "다시 로그인"),
            onAction: () => navigate(
              `/login?returnTo=${encodeURIComponent(returnTo)}`,
              { replace: true },
            ),
          }}
        />
      </main>
    );
  }
  if (!user) {
    const returnTo = `${location.pathname}${location.search}`;
    return <Navigate to={`/login?returnTo=${encodeURIComponent(returnTo)}`} replace />;
  }
  return <Outlet />;
}
