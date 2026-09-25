import { Analytics } from "./Analytics";
import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { useCurrentUser } from "../products/account/app/useCurrentUser";
import { LoginScreen } from "../products/account/iface/LoginScreen";
import App from "./App";
import { AuthBoundary } from "./AuthBoundary";
import { SupportOperatorPage } from "../products/support/iface/SupportOperatorPage";
import { AgencyOrderOperatorPage } from "../products/ordering/iface/AgencyOrderOperatorPage";
import { AgencyOrderInvestigationPage } from "../products/ordering/iface/AgencyOrderInvestigationPage";
import { OrderAccountingPage } from "../products/ordering/iface/OrderAccountingPage";
import { AgencyOrderExceptionsPage } from "../products/ordering/iface/AgencyOrderExceptionsPage";
import { AccountOverviewPage } from "../products/account/iface/AccountOverviewPage";
import { AgencyOrderTrackingPage } from "../products/ordering/iface/AgencyOrderTrackingPage";
import { CurationWorkspacePage } from "../products/curation/iface/CurationWorkspacePage";
import { OperatorAPIUsagePage } from "../products/curation/intelligence/managedrunner/iface/OperatorAPIUsagePage";
import { OperatorOpsPage } from "../products/ops/iface/OperatorOpsPage";
import {
  HomePage,
  NewPlanPage,
  NotFoundPage,
  PlanResumePage,
} from "./canonicalPages";
import { operatorHomePath } from "./operatorHome";
import { useLocale } from "../shared/i18n";
import { RouteLoadBoundary } from "./RouteLoadBoundary";
import { loadRouteModule } from "./routeChunkRecovery";

const OrderSheetPage = lazy(() => loadRouteModule("order-sheet", async () => {
  const module = await import("../products/ordering/iface/OrderSheetPage");
  return { default: module.OrderSheetPage };
}));

const AgencyOrderPaymentPage = lazy(() => loadRouteModule("agency-order-payment", async () => {
  const module = await import("../products/ordering/iface/AgencyOrderPaymentPage");
  return { default: module.AgencyOrderPaymentPage };
}));

export function AppRouter() {
  return (
    <BrowserRouter>
      <Analytics>
      <Routes>
        <Route path="/login" element={<LoginScreen />} />
        <Route element={<AuthBoundary />}>
          <Route element={<App />}>
            <Route index element={<HomePage />} />
            <Route path="account" element={<AccountOverviewPage />} />
	        <Route path="agencyOrder" element={<AgencyOrderTrackingPage />} />
	        <Route path="agencyOrder/:agencyOrderId" element={<AgencyOrderTrackingPage />} />
            <Route path="admin/support" element={<SupportOperatorPage />} />
	        <Route path="admin/agencyOrder" element={<AgencyOrderOperatorPage />} />
	        <Route path="admin/agencyOrder/exceptions" element={<AgencyOrderExceptionsPage />} />
	        <Route path="admin/agencyOrder/:agencyOrderId" element={<AgencyOrderInvestigationPage />} />
            <Route path="admin/order-accounting" element={<OrderAccountingPage />} />
            <Route path="admin/api-usage" element={<OperatorAPIUsagePage />} />
            <Route path="admin/ops" element={<OperatorOpsPage />} />
            <Route path="admin" element={<OperatorHomeRedirect />} />
            <Route path="plans/new" element={<NewPlanPage />} />
            <Route
              path="curations/:curationId"
              element={<CurationWorkspacePage />}
            />
            <Route
              path="curations/:curationId/order-sheet"
              element={(
                <RouteLoadBoundary>
                  <Suspense fallback={<RouteLoading kind="order-sheet" />}>
                    <OrderSheetPage />
                  </Suspense>
                </RouteLoadBoundary>
              )}
            />
            <Route
              path="agencyOrder/:agencyOrderId/payment"
              element={(
                <RouteLoadBoundary>
                  <Suspense fallback={<RouteLoading kind="payment" />}>
                    <AgencyOrderPaymentPage />
                  </Suspense>
                </RouteLoadBoundary>
              )}
            />
            <Route path="plans/:planId" element={<PlanResumePage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Route>
      </Routes>
    </Analytics>
    </BrowserRouter>
  );
}

function RouteLoading({ kind }: { kind: "order-sheet" | "payment" }) {
  const { l } = useLocale();
  return (
    <section className="workspace-card">
      {kind === "order-sheet"
        ? l("Preparing the order sheet…", "주문서를 준비하고 있습니다.")
        : l("Preparing payment instructions…", "결제 지시서를 준비하고 있습니다.")}
    </section>
  );
}

function OperatorHomeRedirect() {
  const { user } = useCurrentUser();
  return <Navigate replace to={operatorHomePath(user ?? {})} />;
}
