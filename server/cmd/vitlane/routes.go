package main

import (
	"fmt"
	"github.com/vitlane/vitlane/server/internal/shared/infra/analytics"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	agencyorderhttp "github.com/vitlane/vitlane/server/internal/ordering/agencyorder/iface/http"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
)

// newRouter registers every HTTP route exactly as the previous single-file
// run() did and returns the cross-origin protected handler.
func newRouter(app *application) (http.Handler, error) {
	config := app.config
	database := app.database
	accountHandler := app.accountHandler
	authHandler := app.authHandler
	authMiddleware := app.authMiddleware
	curationHandler := app.curationHandler
	threadHandler := app.threadHandler
	curationWorkspaceHandler := app.curationWorkspaceHandler
	selectionHandler := app.selectionHandler
	researchHandler := app.researchHandler
	sessionHandler := app.sessionHandler
	managedRunnerHandler := app.managedRunnerHandler
	intelligenceHandler := app.intelligenceHandler
	settlementHandler := app.settlementHandler
	agencyOrderHandler := app.agencyOrderHandler
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// Core readiness only: settlement runtime degradation is reported through
	// the authenticated operator ops health instead (ADR-0040 §3, GAP-007).
	mux.HandleFunc("GET /readyz", newCoreReadinessHandler(database))
	mux.HandleFunc("GET /.well-known/ucp-agent.json", agencyorderhttp.AgentProfile)
	// ADR-0040 §10: sensitive operator actions demand a recent Google
	// re-authentication on top of the operator allowlist.
	freshOperatorRoute := func(handler http.Handler) http.Handler {
		return authMiddleware.RequireFreshOperator(
			handler,
			time.Duration(config.operatorFreshAuthMinutes)*time.Minute,
		)
	}
	mux.HandleFunc("GET /api/v1/analytics/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		httpapi.WriteJSON(w, http.StatusOK, config.analytics.Public())
	})
	mux.HandleFunc("GET /api/v1/auth/capabilities", authHandler.Capabilities)
	mux.HandleFunc("GET /api/v1/auth/google/start", authHandler.BeginGoogleLogin)
	mux.HandleFunc("GET /api/v1/auth/google/callback", authHandler.CompleteGoogleLogin)
	mux.HandleFunc("GET /api/v1/auth/mobile/google/start", authHandler.BeginMobileGoogleLogin)
	mux.HandleFunc("GET /api/v1/auth/mobile/complete", authHandler.CompleteMobileLogin)
	mux.HandleFunc("POST /api/v1/auth/mobile/exchange", authHandler.ExchangeMobileLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)
	mux.HandleFunc("POST /api/v1/auth/logout-all", authHandler.LogoutAll)
	mux.HandleFunc("DELETE /api/v1/account", authHandler.RequestAccountDeletion)
	// This route remains present when AgencyOrder is disabled so the Web never
	// sends a user from a valid Cart into a collection of 404-only endpoints.
	mux.Handle(
		"GET /api/v1/agency-order-capability",
		authMiddleware.RequireSession(agencyorderhttp.Capability(
			agencyOrderHandler != nil,
			config.agencyOrderCheckoutProvider,
			config.paypalSandboxEnabled,
			config.liveAgencyOrderIssueEnabled,
			app.liveControlService,
		)),
	)
	if config.allowDevAuth {
		mux.HandleFunc("POST /api/v1/dev/auth/session", authHandler.CreateDevelopmentSession)
		mux.HandleFunc(
			"POST /api/v1/dev/auth/profiles/{profileKey}/reset",
			authHandler.ResetDevelopmentProfile,
		)
	}
	if app.liveCatalogReviewHandler != nil {
		catalogResearchRoute := authMiddleware.RequireSession
		mux.Handle("GET /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/external-product", catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.ExternalProduct)))
		mux.Handle("PUT /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/external-product/reaction", catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.ProductReaction)))
		mux.Handle("PUT /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/external-product/purchase-check", catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.ExternalProduct)))
		for _, action := range []string{"state", "external-link", "resolve", "purchase-check"} {
			method := "GET"
			if action == "resolve" {
				method = "POST"
			}
			if action == "purchase-check" {
				method = "PUT"
			}
			mux.Handle(method+" /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/amazon/"+action, catalogResearchRoute(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.SetPathValue("amazonAction", action)
				app.liveCatalogReviewHandler.AmazonCandidate(w, r)
			})))
		}
		// Catalog Research is the only public Research data-plane API. Phase-era
		// aliases were removed at the hard cutover instead of being dual-routed.
		mux.Handle(
			"POST /api/v1/curations/{curationId}/catalog-research/hydrations",
			catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.HydrateWorkspace)),
		)
		mux.Handle(
			"POST /api/v1/curations/{curationId}/targets/{targetId}/catalog-research/expansions",
			catalogResearchRoute(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpapi.WriteError(w, http.StatusGone, "RESEARCH_EXPAND_REMOVED", "Use Research Again to discover more products")
			})),
		)
		mux.Handle(
			"POST /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/variant-pages",
			catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.BrowseWorkspaceVariants)),
		)
		mux.Handle(
			"PUT /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/configuration",
			catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.SaveWorkspaceConfiguration)),
		)
		mux.Handle(
			"PUT /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/variants/{variantId}/interaction",
			catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.SaveWorkspaceInteraction)),
		)
		mux.Handle(
			"POST /api/v1/curations/{curationId}/prepare-agency-order",
			catalogResearchRoute(http.HandlerFunc(app.liveCatalogReviewHandler.PrepareWorkspaceAgencyOrder)),
		)
	}
	mux.Handle("GET /api/v1/me", authMiddleware.RequireSession(http.HandlerFunc(authHandler.Me)))
	browserRunRoute := authMiddleware.RequireSession
	mux.Handle(
		"POST /api/v1/curations/{curationId}/catalog-research/candidates/{candidateId}/browser-runs",
		browserRunRoute(http.HandlerFunc(app.browserRunHandler.Create)),
	)
	mux.Handle("GET /api/v1/browser-runs/{runId}", browserRunRoute(http.HandlerFunc(app.browserRunHandler.Get)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/navigation-approvals", browserRunRoute(http.HandlerFunc(app.browserRunHandler.ApproveNavigation)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/preparation-approvals", browserRunRoute(http.HandlerFunc(app.browserRunHandler.ApprovePreparation)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/observations", browserRunRoute(http.HandlerFunc(app.browserRunHandler.RecordObservation)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/handoffs", browserRunRoute(http.HandlerFunc(app.browserRunHandler.RequireHandoff)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/takeover", browserRunRoute(http.HandlerFunc(app.browserRunHandler.TakeOver)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/pause", browserRunRoute(http.HandlerFunc(app.browserRunHandler.Pause)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/resume-requests", browserRunRoute(http.HandlerFunc(app.browserRunHandler.RequestResume)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/cancellation", browserRunRoute(http.HandlerFunc(app.browserRunHandler.Cancel)))
	mux.Handle("POST /api/v1/browser-runs/{runId}/result-verifications", browserRunRoute(http.HandlerFunc(app.browserRunHandler.VerifyResult)))
	if agencyOrderHandler != nil {
		agencyOrderRoute := authMiddleware.RequireSession
		mux.Handle("POST /api/v1/curations/{curationId}/order-sheets", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.CreateOrderSheet)))
		mux.Handle("GET /api/v1/order-sheets/{orderSheetId}", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.GetOrderSheet)))
		mux.Handle("PUT /api/v1/order-sheets/{orderSheetId}/shipping-address", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.SetShippingAddress)))
		mux.Handle("PUT /api/v1/order-sheets/{orderSheetId}/delivery-selections", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.SelectDelivery)))
		mux.Handle("POST /api/v1/order-sheets/{orderSheetId}/preflight", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.Preflight)))
		mux.Handle("POST /api/v1/order-sheets/{orderSheetId}/issue", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.Issue)))
		mux.Handle("GET /api/v1/agencyOrder", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.ListOrders)))
		mux.Handle("GET /api/v1/agencyOrder/{agencyOrderId}", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.GetOrder)))
		mux.Handle("GET /api/v1/agencyOrder/{agencyOrderId}/process-requests/{requestId}", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.RequestReceipt)))
		mux.Handle("GET /api/v1/agencyOrder/{agencyOrderId}/process-requests", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.ListProcessRequests)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/shipping-address-reveal", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.RevealOrderShipping)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/settlement-authorizations", agencyOrderRoute(http.HandlerFunc(settlementHandler.AuthorizeAgencyOrder)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/wallet-transactions", agencyOrderRoute(http.HandlerFunc(settlementHandler.SubmitAgencyOrderWalletTransaction)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/settlement-transactions", agencyOrderRoute(http.HandlerFunc(settlementHandler.SubmitAgencyOrderPayTransaction)))
		mux.Handle("GET /api/v1/agencyOrder/{agencyOrderId}/settlement", agencyOrderRoute(http.HandlerFunc(settlementHandler.GetAgencyOrderPayment)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/refund-intents", agencyOrderRoute(http.HandlerFunc(settlementHandler.CreateAgencyOrderRefundIntent)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/refund-transactions", agencyOrderRoute(http.HandlerFunc(settlementHandler.SubmitAgencyOrderRefundTransaction)))
		mux.Handle("GET /api/v1/agencyOrder/{agencyOrderId}/receipt", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.GetReceipt)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/refund-requests", agencyOrderRoute(http.HandlerFunc(agencyOrderHandler.RequestRefund)))
		// 결제 후·첫 merchant effect 전 자유 취소(ADR-0052 §2.4).
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/cancellation", agencyOrderRoute(http.HandlerFunc(app.procurementHandler.CancelOrder)))
		// 30일 지연 rule 무료 취소(FTC, ADR-0052 §2.5) — 항상 GROSS.
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/delay-cancellation", agencyOrderRoute(http.HandlerFunc(app.procurementHandler.CancelDelayRule)))
		mux.Handle("GET /api/v1/agencyOrder/{agencyOrderId}/procurement-requests", agencyOrderRoute(http.HandlerFunc(app.procurementHandler.ListCustomerRequests)))
		mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/procurement-requests/{requestId}/response", agencyOrderRoute(http.HandlerFunc(app.procurementHandler.RespondCustomerRequest)))
		if app.paymentHandler != nil {
			mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/paypal/checkout", agencyOrderRoute(http.HandlerFunc(app.paymentHandler.StartCheckout)))
			mux.Handle("POST /api/v1/agencyOrder/{agencyOrderId}/paypal/resume", agencyOrderRoute(http.HandlerFunc(app.paymentHandler.Resume)))
			mux.Handle("GET /api/v1/agencyOrder/{agencyOrderId}/paypal", agencyOrderRoute(http.HandlerFunc(app.paymentHandler.GetView)))
			// webhook은 서명 검증이 인증이다 — 세션 없이 받되 서명 실패는 거절한다.
			mux.HandleFunc("POST /api/webhooks/paypal/sandbox", app.paymentHandler.Webhook)
			mux.HandleFunc("POST /api/webhooks/paypal/live", app.paymentHandler.LiveWebhook)
		}
		operatorRoute := func(handler http.Handler) http.Handler {
			return authMiddleware.RequireSettlementOperator(handler)
		}
		mux.Handle("GET /api/v1/admin/liveControl",
			operatorRoute(http.HandlerFunc(app.liveControlHandler.Get)))
		mux.Handle("POST /api/v1/admin/liveControl/kill",
			freshOperatorRoute(http.HandlerFunc(app.liveControlHandler.Kill)))
		if app.paymentHandler != nil {
			mux.Handle("GET /api/v1/admin/payment/paypal/disputes", operatorRoute(http.HandlerFunc(app.paymentHandler.ListPayPalDisputes)))
			mux.Handle("GET /api/v1/admin/payment/paypal/disputes/{caseId}", operatorRoute(http.HandlerFunc(app.paymentHandler.GetPayPalDispute)))
			mux.Handle("POST /api/v1/admin/payment/paypal/disputes/{caseId}/actions", freshOperatorRoute(http.HandlerFunc(app.paymentHandler.RecordPayPalDisputeAction)))
			mux.Handle("POST /api/v1/admin/payment/paypal/merchant-orders/{merchantOrderId}/reauthorization-adoptions", freshOperatorRoute(http.HandlerFunc(app.paymentHandler.AdoptMOReauthorization)))
			mux.Handle("POST /api/v1/admin/payment/paypal/mo-compensations/{compensationId}/refund-adoptions", freshOperatorRoute(http.HandlerFunc(app.paymentHandler.AdoptPayPalMORefund)))
		}
		// Procurement 운영자 work surface(계약 v7 §14 PROCUREMENT_READY) —
		// 실행 단위는 MerchantOrder(Shop-checkout) Task다(ADR-0052).
		mux.Handle("GET /api/v1/admin/procurement/queue", operatorRoute(http.HandlerFunc(app.procurementHandler.ListQueue)))
		mux.Handle("POST /api/v1/admin/procurement/tasks/{taskId}/claim", operatorRoute(http.HandlerFunc(app.procurementHandler.ClaimTask)))
		mux.Handle("POST /api/v1/admin/procurement/tasks/{taskId}/shipping-address-reveals", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.RevealShipping)))
		mux.Handle("GET /api/v1/admin/procurement/tasks/{taskId}/process-requests/{requestId}/reveal", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.RevealResult)))
		mux.Handle("POST /api/v1/admin/procurement/tasks/{taskId}/continue-url-reveals", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.RevealContinueURL)))
		mux.Handle("GET /api/v1/admin/procurement/tasks/{taskId}/manual-review", operatorRoute(http.HandlerFunc(app.procurementHandler.ReviewSurface)))
		mux.Handle("POST /api/v1/admin/procurement/tasks/{taskId}/decisions", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.RecordManualDecision)))
		mux.Handle("POST /api/v1/admin/procurement/tasks/{taskId}/customer-requests", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.CreateCustomerRequest)))
		mux.Handle("POST /api/v1/admin/procurement/customer-requests/{requestId}/resolution", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.ResolveCustomerRequest)))
		mux.Handle("POST /api/v1/admin/procurement/tasks/{taskId}/merchant-effect", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.BeginMerchantEffect)))
		mux.Handle("POST /api/v1/admin/procurement/tasks/{taskId}/result", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.RecordResult)))
		// 간이 회수 원장 기입(운영정합 5차 PR-D) — 목록은 일반 운영자,
		// 금액 변경·삭제는 fresh 인증을 요구한다.
		mux.Handle("GET /api/v1/admin/procurement/recovery-entries", operatorRoute(http.HandlerFunc(app.procurementHandler.ListRecovery)))
		mux.Handle("POST /api/v1/admin/procurement/recovery-entries", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.CreateRecovery)))
		mux.Handle("POST /api/v1/admin/procurement/recovery-entries/{entryId}/record", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.RecordRecovery)))
		mux.Handle("POST /api/v1/admin/procurement/recovery-entries/{entryId}/waive", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.WaiveRecovery)))
		mux.Handle("DELETE /api/v1/admin/procurement/recovery-entries/{entryId}", freshOperatorRoute(http.HandlerFunc(app.procurementHandler.DeleteRecovery)))
		// Logistics 운영자 work surface(계약 v7 §9) — LIVE PLACED 주문의
		// shipment 등록·tracking evidence·수령 일괄 확인.
		mux.Handle("POST /api/v1/admin/logistics/shipments", freshOperatorRoute(http.HandlerFunc(app.logisticsHandler.CreateShipment)))
		mux.Handle("POST /api/v1/admin/logistics/shipments/{shipmentId}/events", freshOperatorRoute(http.HandlerFunc(app.logisticsHandler.RecordEvent)))
		mux.Handle("POST /api/v1/admin/logistics/shipments/{shipmentId}/delivered-confirmation", freshOperatorRoute(http.HandlerFunc(app.logisticsHandler.ConfirmDelivered)))
		mux.Handle("GET /api/v1/admin/logistics/agencyOrder/{agencyOrderId}/shipments", operatorRoute(http.HandlerFunc(app.logisticsHandler.ListOrderShipments)))
		// 배송 예외 판정(write-once)·수동 회수 lane(Step 5B, §9.1·§9.3).
		mux.Handle("GET /api/v1/admin/logistics/exceptions", operatorRoute(http.HandlerFunc(app.logisticsHandler.ListExceptionUnits)))
		mux.Handle("POST /api/v1/admin/logistics/units/{expectedUnitId}/resolution", freshOperatorRoute(http.HandlerFunc(app.logisticsHandler.ResolveException)))
		mux.Handle("GET /api/v1/admin/logistics/returns", operatorRoute(http.HandlerFunc(app.logisticsHandler.ListReturns)))
		mux.Handle("POST /api/v1/admin/logistics/returns", freshOperatorRoute(http.HandlerFunc(app.logisticsHandler.CreateReturn)))
		mux.Handle("POST /api/v1/admin/logistics/returns/{returnId}", freshOperatorRoute(http.HandlerFunc(app.logisticsHandler.UpdateReturn)))
		mux.Handle("GET /api/v1/admin/agencyOrder/refund-requests", operatorRoute(http.HandlerFunc(agencyOrderHandler.ListRefundQueue)))
		// 통합 work surface 목록(ADR-0055 §5) — 명령은 위 owner endpoint가 소유.
		mux.Handle("GET /api/v1/admin/ordering/work-items", operatorRoute(http.HandlerFunc(app.operatorWorkHandler.ListWorkItems)))
		mux.Handle("GET /api/v1/admin/ordering/work-items/counts", operatorRoute(http.HandlerFunc(app.operatorWorkHandler.ListWorkItemCounts)))
		mux.Handle("POST /api/v1/admin/ordering/order-lookups", operatorRoute(http.HandlerFunc(app.operatorWorkHandler.LookupOrder)))
		mux.Handle("GET /api/v1/admin/ordering/orders/{agencyOrderId}", operatorRoute(http.HandlerFunc(app.operatorWorkHandler.GetOrderInvestigation)))
		mux.Handle("GET /api/v1/admin/ordering/orders/{agencyOrderId}/timeline", operatorRoute(http.HandlerFunc(app.operatorWorkHandler.GetOrderTimeline)))
		mux.Handle("GET /api/v1/admin/ordering/order-accounting", operatorRoute(http.HandlerFunc(app.accountingHandler.GetAccounting)))
		mux.Handle("POST /api/v1/admin/ordering/process-effects/{effectId}/retry", operatorRoute(http.HandlerFunc(app.operatorWorkHandler.RetryProcessEffect)))
		mux.Handle("GET /api/v1/admin/ordering/orders/{agencyOrderId}/process-requests/{requestId}", operatorRoute(http.HandlerFunc(app.operatorWorkHandler.ProcessRequestReceipt)))
		mux.Handle("POST /api/v1/admin/agencyOrder/refund-requests/{requestId}/decisions", freshOperatorRoute(http.HandlerFunc(agencyOrderHandler.DecideRefundRequest)))
	}
	// Support 대화(ADR-0059) — 제품 무관하게 항상 열린다. 종전 고지함 라우트
	// (GET /notices·notices/{id}/read·admin notices 발행)는 이 대화로 흡수됐다.
	mux.Handle("GET /api/v1/support/messages", authMiddleware.RequireSession(http.HandlerFunc(app.supportHandler.ListMessages)))
	mux.Handle("POST /api/v1/support/messages", authMiddleware.RequireSession(http.HandlerFunc(app.supportHandler.SendMessage)))
	mux.Handle("GET /api/v1/support/images/{attachmentId}", authMiddleware.RequireSession(http.HandlerFunc(app.supportHandler.DownloadImage)))
	mux.Handle("POST /api/v1/support/messages/read", authMiddleware.RequireSession(http.HandlerFunc(app.supportHandler.MarkRead)))
	mux.Handle("GET /api/v1/support/summary", authMiddleware.RequireSession(http.HandlerFunc(app.supportHandler.Summary)))
	mux.Handle("GET /api/v1/admin/support/conversations", authMiddleware.RequireOperator(http.HandlerFunc(app.supportHandler.ListConversations)))
	mux.Handle("GET /api/v1/admin/support/conversations/{userId}/messages", authMiddleware.RequireOperator(http.HandlerFunc(app.supportHandler.OperatorThread)))
	mux.Handle("POST /api/v1/admin/support/conversations/{userId}/messages", freshOperatorRoute(http.HandlerFunc(app.supportHandler.ReplyOperator)))
	mux.Handle("GET /api/v1/admin/support/conversations/{userId}/images/{attachmentId}", freshOperatorRoute(http.HandlerFunc(app.supportHandler.DownloadImageForOperator)))
	mux.Handle("PUT /api/v1/admin/support/conversations/{userId}/messages/{messageId}/no-reply-resolution", freshOperatorRoute(http.HandlerFunc(app.supportHandler.HandleWithoutReply)))
	mux.Handle("POST /api/v1/admin/support/orders/{agencyOrderId}/messages", freshOperatorRoute(http.HandlerFunc(app.supportHandler.SendOrderMessage)))
	mux.Handle("GET /api/v1/admin/support/counts", authMiddleware.RequireOperator(http.HandlerFunc(app.supportHandler.Counts)))
	if config.settlementEnabled {
		settlementRoute := authMiddleware.RequireSession
		mux.Handle("POST /api/v1/account/wallet-registration-attempts", settlementRoute(http.HandlerFunc(accountHandler.CreateWalletRegistrationAttempt)))
		mux.Handle("POST /api/v1/account/wallet-registration-attempts/{attemptId}/complete", settlementRoute(http.HandlerFunc(accountHandler.CompleteWalletRegistrationAttempt)))
		mux.Handle("POST /api/v1/account/wallets/{walletId}/deregister", settlementRoute(http.HandlerFunc(accountHandler.DeregisterWallet)))
		mux.Handle("POST /api/v1/account/wallets/{walletId}/kyc-cases", settlementRoute(http.HandlerFunc(accountHandler.StartKYCVerification)))
		mux.Handle("POST /api/v1/account/kyc-cases/{caseId}/check", settlementRoute(http.HandlerFunc(accountHandler.CheckKYC)))
		mux.Handle("PUT /api/v1/account/shipping-profiles/default", settlementRoute(http.HandlerFunc(accountHandler.SaveDefaultShippingProfile)))
		mux.Handle("DELETE /api/v1/account/shipping-profiles/{profileId}", settlementRoute(http.HandlerFunc(accountHandler.RetireShippingProfile)))
		mux.Handle("POST /api/v1/account/shipping-profiles/{profileId}/reveal", settlementRoute(http.HandlerFunc(accountHandler.RevealShippingProfile)))
		mux.Handle("POST /api/v1/account/policy-acceptances/test-settlement", settlementRoute(http.HandlerFunc(accountHandler.AcceptTestSettlementPolicy)))
		mux.Handle("GET /api/v1/settlement/config", settlementRoute(http.HandlerFunc(settlementHandler.Config)))
	}
	mux.Handle("GET /api/v1/account/overview", authMiddleware.RequireSession(http.HandlerFunc(accountHandler.AccountOverview)))
	mux.Handle("GET /api/v1/me/preferences", authMiddleware.RequireSession(http.HandlerFunc(accountHandler.Preferences)))
	mux.Handle("POST /api/v1/curations/{curationId}/budget/proposals", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.BudgetProposal)))
	mux.Handle("GET /api/v1/curations/{curationId}/budget", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.Budget)))
	mux.Handle("PATCH /api/v1/curations/{curationId}/budget", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.Budget)))
	mux.Handle("GET /api/v1/curations/{curationId}/targets/{targetId}/criteria", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.Criteria)))
	mux.Handle("PUT /api/v1/curations/{curationId}/targets/{targetId}/criteria", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.Criteria)))
	mux.Handle("GET /api/v1/curations/{curationId}/research-settings", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.ResearchSettings)))
	mux.Handle("PATCH /api/v1/curations/{curationId}/research-settings", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.ResearchSettings)))
	mux.Handle("PATCH /api/v1/me/preferences", authMiddleware.RequireSession(http.HandlerFunc(accountHandler.Preferences)))
	mux.Handle("GET /api/v1/managed-runner/capability", authMiddleware.RequireSession(http.HandlerFunc(managedRunnerHandler.Capability)))
	mux.Handle("GET /api/v1/managed-runner/usage", authMiddleware.RequireSession(http.HandlerFunc(managedRunnerHandler.Usage)))
	if app.catalogAPIHandler != nil {
		mux.Handle("GET /api/v1/admin/catalog-apis", authMiddleware.RequireOperator(http.HandlerFunc(app.catalogAPIHandler.Usage)))
		mux.Handle("GET /api/v1/admin/catalog-apis/{apiId}/usage", authMiddleware.RequireOperator(http.HandlerFunc(app.catalogAPIHandler.Usage)))
		mux.Handle("GET /api/v1/admin/catalog-apis/round-summary", authMiddleware.RequireOperator(http.HandlerFunc(app.catalogAPIHandler.RoundSummary)))
		mux.Handle("PUT /api/v1/admin/catalog-apis/{apiId}/control", freshOperatorRoute(http.HandlerFunc(app.catalogAPIHandler.SetControl)))
		mux.Handle("GET /api/v1/curations/{curationId}/exchange-rate", authMiddleware.RequireSession(http.HandlerFunc(app.catalogAPIHandler.ExchangeRate)))
	}
	if app.amazonUsageHandler != nil {
		mux.Handle("GET /api/v1/admin/catalog-sources/amazon/usage", authMiddleware.RequireOperator(http.HandlerFunc(app.amazonUsageHandler.Usage)))
		mux.Handle("PUT /api/v1/admin/catalog-sources/amazon/control", freshOperatorRoute(http.HandlerFunc(app.amazonUsageHandler.SetControl)))
	}
	mux.Handle("GET /api/v1/admin/managed-runner/usage", authMiddleware.RequireOperator(http.HandlerFunc(managedRunnerHandler.AdminUsage)))
	// GAP-024: audited operator resolution of UNKNOWN cost reservations.
	mux.Handle(
		"POST /api/v1/admin/managed-runner/reservations/{reservationId}/resolutions",
		freshOperatorRoute(
			http.HandlerFunc(managedRunnerHandler.ResolveUnknownReservation),
		),
	)
	// Registered regardless of settlementEnabled: the core section always reports
	// and the settlement section appears only when the runtime is active.
	mux.Handle("GET /api/v1/admin/ops/health", authMiddleware.RequireOperator(
		newOpsHealthHandler(app.opsReporter),
	))
	// L1 view for the dashboard: Healthchecks project state through the
	// server-held read-only key, so the browser never sees ping URLs.
	mux.Handle("GET /api/v1/admin/ops/heartbeats", authMiddleware.RequireOperator(
		newOpsHeartbeatsHandler(app.heartbeats),
	))
	// Time series behind the dashboard charts (five-minute sampler).
	mux.Handle("GET /api/v1/admin/ops/samples", authMiddleware.RequireOperator(
		newOpsSamplesHandler(database),
	))
	// ADR-0040 §10: session visibility and the audited force-revoke command.
	mux.Handle("GET /api/v1/admin/ops/sessions", authMiddleware.RequireOperator(
		http.HandlerFunc(app.operatorSessionsHandler.List),
	))
	mux.Handle(
		"POST /api/v1/admin/ops/users/{userId}/session-revocations",
		freshOperatorRoute(
			http.HandlerFunc(app.operatorSessionsHandler.RevokeUser),
		),
	)
	// ADR-0038 folds job progress into the workspace projection, so the browser
	// polls one endpoint instead of three.
	mux.Handle("POST /api/v1/intelligence/jobs/{jobId}/retry", authMiddleware.RequireSession(http.HandlerFunc(intelligenceHandler.Retry)))
	mux.Handle("POST /api/v1/curation-actions/{actionId}/cancel", authMiddleware.RequireSession(http.HandlerFunc(intelligenceHandler.CancelAction)))
	if app.backgroundHandler != nil {
		mux.Handle("GET /api/v1/curations/{curationId}/background-research", authMiddleware.RequireSession(http.HandlerFunc(app.backgroundHandler.View)))
		mux.Handle("POST /api/v1/curations/{curationId}/subscriptions/{subscriptionId}/cancel", authMiddleware.RequireSession(http.HandlerFunc(app.backgroundHandler.Cancel)))
		mux.Handle("POST /api/v1/curations/{curationId}/findings/{findingId}/hide", authMiddleware.RequireSession(http.HandlerFunc(app.backgroundHandler.Hide)))
		mux.Handle("POST /api/v1/curations/{curationId}/findings/{findingId}/candidates", authMiddleware.RequireSession(http.HandlerFunc(app.backgroundHandler.Import)))
		mux.Handle("POST /api/v1/curations/product-notices/sync", authMiddleware.RequireSession(http.HandlerFunc(app.backgroundHandler.Notices)))
	}
	mux.Handle("GET /api/v1/curations", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.ListCurations)))
	mux.Handle("POST /api/v1/shopping-plans", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.CreatePlan)))
	mux.Handle("GET /api/v1/shopping-plans/{planId}", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.GetPlan)))
	mux.Handle("GET /api/v1/curations/{curationId}/available-actions", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.GetAvailableCurationActions)))
	mux.Handle("GET /api/v1/curations/{curationId}/actions", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.ListCurationActions)))
	mux.Handle("POST /api/v1/curations/{curationId}/conversation-requests", authMiddleware.RequireSession(http.HandlerFunc(app.conversationHandler.Execute)))
	mux.Handle("POST /api/v1/curations/{curationId}/follow-ups/{messageId}/responses", authMiddleware.RequireSession(http.HandlerFunc(app.conversationHandler.Respond)))
	mux.Handle("GET /api/v1/curations/{curationId}/control-mode", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.GetMode)))
	mux.Handle("PUT /api/v1/curations/{curationId}/control-mode", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.SetMode)))
	mux.Handle("GET /api/v1/curations/{curationId}/threads/{threadId}/combination", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.CombinationStatus)))
	mux.Handle("POST /api/v1/curations/{curationId}/representative-cart", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.ApplyRepresentativeCart)))
	mux.Handle("POST /api/v1/curations/{curationId}/threads/{threadId}/combination-cart", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.ApplyCombinationCart)))
	mux.Handle("GET /api/v1/curations/{curationId}/threads", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.List)))
	mux.Handle("POST /api/v1/curations/{curationId}/threads", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.Submit)))
	mux.Handle("POST /api/v1/curations/{curationId}/threads/{threadId}/cancel", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.Cancel)))
	mux.Handle("POST /api/v1/curations/{curationId}/threads/{threadId}/actions/{actionId}/cancel", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.CancelAction)))
	mux.Handle("POST /api/v1/curations/{curationId}/threads/{threadId}/answer", authMiddleware.RequireSession(http.HandlerFunc(threadHandler.Answer)))
	mux.Handle("PUT /api/v1/curations/{curationId}/actions/{actionId}/target-remove", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.ExecuteTargetRemoveAction)))
	mux.Handle("GET /api/v1/curations/{curationId}/workspace", authMiddleware.RequireSession(http.HandlerFunc(curationWorkspaceHandler.Get)))
	mux.Handle("POST /api/v1/shopping-plans/{planId}/expansions", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.CreateExpansion)))
	mux.Handle("GET /api/v1/shopping-plans/{planId}/expansions/current", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.GetCurrentExpansion)))
	mux.Handle("GET /api/v1/shopping-plans/{planId}/expansions/{runId}", authMiddleware.RequireSession(http.HandlerFunc(curationHandler.GetExpansion)))
	mux.Handle("POST /api/v1/shopping-plans/{planId}/research-rounds", authMiddleware.RequireSession(http.HandlerFunc(researchHandler.StartResearch)))
	mux.Handle("GET /api/v1/account/liked-variants", authMiddleware.RequireSession(http.HandlerFunc(researchHandler.ListLikedVariantsV2)))
	mux.Handle("GET /api/v1/account/purchase-checks", authMiddleware.RequireSession(http.HandlerFunc(researchHandler.ListPurchaseChecks)))
	mux.Handle("GET /api/v1/curations/{curationId}/cart", authMiddleware.RequireSession(http.HandlerFunc(app.catalogCartHandler.Get)))
	mux.Handle("PUT /api/v1/curations/{curationId}/cart", authMiddleware.RequireSession(http.HandlerFunc(app.catalogCartHandler.Replace)))
	mux.Handle("POST /api/v1/curations/{curationId}/selections", authMiddleware.RequireSession(http.HandlerFunc(selectionHandler.CreateSelection)))
	mux.Handle("PUT /api/v1/curations/{curationId}/selections/{selectionId}", authMiddleware.RequireSession(http.HandlerFunc(selectionHandler.UpdateSelection)))
	mux.Handle("DELETE /api/v1/curations/{curationId}/selections/{selectionId}", authMiddleware.RequireSession(http.HandlerFunc(selectionHandler.RemoveSelection)))
	mux.Handle("GET /api/v1/shopping-sessions/{sessionId}", authMiddleware.RequireSession(http.HandlerFunc(sessionHandler.GetSession)))
	mux.Handle("POST /api/v1/shopping-sessions/{sessionId}/research/cancel", authMiddleware.RequireSession(http.HandlerFunc(researchHandler.Cancel)))
	mux.Handle("POST /api/v1/shopping-sessions/{sessionId}/research-again", authMiddleware.RequireSession(http.HandlerFunc(researchHandler.ResearchAgain)))

	if webDir := strings.TrimSpace(os.Getenv("WEB_DIR")); webDir != "" {
		mux.Handle("/", frontendHandler(
			spaHandler(webDir),
			staticSiteHandler(config.marketingWebDir, config.analytics.Mode == "ga4"),
			config.marketingHost,
		))
	}

	crossOrigin := http.NewCrossOriginProtection()
	crossOrigin.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteError(w, http.StatusForbidden, "CSRF_ORIGIN_INVALID", "허용되지 않은 요청 출처입니다.")
	}))
	for _, origin := range config.trustedOrigins {
		if err := crossOrigin.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("configure trusted origin %q: %w", origin, err)
		}
	}

	var handler http.Handler = mux
	if app.analytics != nil {
		handler = httpapi.OptionalAnalytics(handler, func(meta httpapi.AnalyticsRequest, event sharedapp.AnalyticsEvent) {
			app.analytics.Submit(analytics.Context{ClientID: meta.ClientID, SessionID: meta.SessionID, Locale: meta.Locale}, event)
		})
	}
	return crossOrigin.Handler(handler), nil
}

func spaHandler(webDir string) http.Handler {
	files := http.FileServer(http.Dir(webDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unknown API paths are retired or invalid contracts, not client-side
		// routes. Never disguise them as a successful SPA document response.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set(
			"Content-Security-Policy",
			"img-src 'self' https: data:; object-src 'none'; base-uri 'self'",
		)
		w.Header().Set("Referrer-Policy", "no-referrer")
		// ADR-0080: search traffic belongs on the Marketing pages that carry
		// hreflang, not on an App screen.
		w.Header().Set("X-Robots-Tag", "noindex")
		path := filepath.Join(webDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil {
			if !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
			if nestedIndex, err := os.Stat(filepath.Join(path, "index.html")); err == nil &&
				!nestedIndex.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFile(w, r, filepath.Join(webDir, "index.html"))
	})
}

func staticSiteHandler(siteDir string, analyticsEnabled ...bool) http.Handler {
	if siteDir == "" {
		return nil
	}
	files := http.FileServer(http.Dir(siteDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := "default-src 'self'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' https: data:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
		if len(analyticsEnabled) > 0 && analyticsEnabled[0] {
			policy = strings.Replace(policy, "script-src 'self'", "script-src 'self' https://www.googletagmanager.com", 1)
			policy = strings.Replace(policy, "connect-src 'self'", "connect-src 'self' https://*.google-analytics.com https://www.googletagmanager.com", 1)
		}
		w.Header().Set("Content-Security-Policy", policy)
		w.Header().Set("Referrer-Policy", "no-referrer")
		if _, adaptive := koreanMarketingPages[r.URL.Path]; adaptive {
			w.Header().Set("Vary", "Accept-Language, Cookie, Sec-Fetch-Site")
			w.Header().Set("Cache-Control", "no-cache")
		}
		if target, ok := koreanMarketingRedirect(r); ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		cleanPath := filepath.Clean(r.URL.Path)
		path := filepath.Join(siteDir, cleanPath)
		if info, err := os.Stat(path); err == nil {
			if info.IsDir() {
				path = filepath.Join(path, "index.html")
			}
			if _, err := os.Stat(path); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

// koreanMarketingPages maps each English canonical page to its Korean pair.
var koreanMarketingPages = map[string]string{
	"/":         "/ko/",
	"/privacy/": "/ko/privacy/",
	"/terms/":   "/ko/terms/",
}

// koreanMarketingRedirect moves a visitor who resolves to Korean (ADR-0080) from
// an English canonical page to its Korean pair, only on arrival from outside
// the site. Korean pages never move, and an in-site navigation such as the
// language link keeps the page the visitor asked for. Crawlers send neither
// cookies nor Accept-Language, so they always read the English page.
func koreanMarketingRedirect(r *http.Request) (string, bool) {
	target, adaptive := koreanMarketingPages[r.URL.Path]
	if !adaptive || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return "", false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "same-site":
		return "", false
	}
	if httpapi.LocaleHintFromRequest(r).UILocale() != sharedapp.UILocaleKorean {
		return "", false
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	return target, true
}

func frontendHandler(product, marketing http.Handler, marketingHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHost := r.Host
		if host, _, err := net.SplitHostPort(r.Host); err == nil {
			requestHost = host
		}
		if marketing != nil && strings.EqualFold(requestHost, marketingHost) {
			marketing.ServeHTTP(w, r)
			return
		}
		product.ServeHTTP(w, r)
	})
}

// One bounded assessment is synchronous; all other routes keep the configured
// interactive timeout. Disconnect cancellation still reaches the provider.
func findingImportTimeout(r *http.Request) time.Duration {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == "POST" && len(parts) == 7 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "curations" && parts[4] == "findings" && parts[6] == "candidates" {
		return time.Minute
	}
	return 0
}
