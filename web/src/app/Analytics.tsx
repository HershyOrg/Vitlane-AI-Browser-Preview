import { useEffect, useState, type ReactNode } from "react";
import { useLocation } from "react-router";
import { useCurrentUser } from "../products/account/app/useCurrentUser";
import { useLocale } from "../shared/i18n";
import { AnalyticsConsent } from "../shared/analytics/AnalyticsConsent";
import { analyticsChanged, analyticsConfig, hasAnalyticsIdentity, consent, consumeAnalyticsLogin, screenFor, startAnalytics, track } from "../shared/analytics/analytics";

export function Analytics({ children }: { children: ReactNode }) {
  const { locale, l } = useLocale();
  const { user, loading, refreshAnalyticsIdentity } = useCurrentUser();
  const location = useLocation();
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const update = () => setRevision(v => v + 1);
    window.addEventListener(analyticsChanged, update);
    return () => window.removeEventListener(analyticsChanged, update);
  }, []);
  useEffect(() => { void startAnalytics("app", locale); }, [locale]);
  const choice = consent();
  const mode = analyticsConfig()?.mode;
  useEffect(() => {
    if (choice === "allowed" && mode === "ga4" && user && !user.marketingAdmin && !user.phase5Operator && !hasAnalyticsIdentity()) void refreshAnalyticsIdentity();
  }, [choice, mode, user?.id, user?.marketingAdmin, user?.phase5Operator, refreshAnalyticsIdentity]);
  useEffect(() => {
    if (loading || (user && analyticsConfig()?.mode === "ga4" && !hasAnalyticsIdentity())) return;
    const screen = screenFor(location.pathname);
    if (screen) track({ name: "page_view", screen }, "route:" + location.key);
    consumeAnalyticsLogin();
  }, [loading, location.pathname, location.key, locale, revision]);
  return <div className="analytics-frame">
    <AnalyticsConsent l={l} showSettings={!user} />
    <div className="analytics-content">{children}</div>
  </div>;
}
