import { createRoot } from "react-dom/client";
import { LocaleProvider } from "../../src/shared/i18n";
import { CurrentUserProvider, useCurrentUser } from "../../src/products/account/app/useCurrentUser";
import { PreferencesProvider, usePreferences } from "../../src/products/account/app/usePreferences";
import { PlanCreator } from "../../src/products/curation/planning/iface/PlanCreator";
import { usePlanFlow } from "../../src/products/curation/planning/app/usePlanFlow";
import "../../src/styles.css";
import "../../src/product-shell.css";
import "../../src/order-operations.css";
import "../../src/catalog-surfaces.css";
import "../../src/account-surfaces.css";

function Init() {
  const preferences = usePreferences();
  const flow = usePlanFlow();
  return <>
    <button id="refresh-preferences" onClick={() => void preferences.refresh()}>Refresh fixture preferences</button>
    <output id="preferences" style={{ display: "block", overflowWrap: "anywhere" }}>{JSON.stringify({ ready: preferences.ready, ...preferences.values })}</output>
    <PlanCreator initialForm={flow.initialForm} working={flow.working} onSubmit={async form => { await flow.submitPlan(form); }} />
    <output id="plan-result" style={{ display: "block", overflowWrap: "anywhere" }}>{JSON.stringify(flow.plan)}</output>
  </>;
}

function AuthenticatedInit() {
  const { user, loading } = useCurrentUser();
  return !loading && user ? <Init /> : null;
}

createRoot(document.getElementById("root")!).render(
  <LocaleProvider><CurrentUserProvider><PreferencesProvider><AuthenticatedInit /></PreferencesProvider></CurrentUserProvider></LocaleProvider>,
);
