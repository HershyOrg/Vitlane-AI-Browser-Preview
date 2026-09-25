import { submitProcessAction } from "./orderProcessApi";
import { request } from "../../../shared/api/client";
import type {
  AuthorizationRecord,
  SettlementPayment,
} from "../../payment/giwa/infra/settlementApi";

export type Money = { amountMinor: number; currency: "USD" };
export type OrderLine = {
  lineId: string;
  sourceCartItemId: string;
  planTargetId: string;
  candidateId: string;
  productTitle: string;
  productUrl?: string;
  imageUrl?: string;
  variantTitle: string;
  selectedOptions: string[];
  quantity: number;
  unitPrice: Money;
  lineSubtotal: Money;
  shopDomain: string;
};
export type MerchantCheckout = {
  merchantId: string;
  shopDomain: string;
  deliveryGroups: Array<{
    id: string;
    lineRefs: string[];
    options: Array<{ id: string; title: string; amountMinor: number; currency: string }>;
    selectedOptionRef?: string;
  }>;
  providerStatus: string;
  providerNotices?: Array<{
    source: "MESSAGE" | "REQUIREMENT";
    type?: string;
    severity?: string;
    code?: string;
    safePath?: string;
    text?: string;
    presentation: "NOTICE" | "DISCLOSURE" | "INTERNAL";
    audience: "CUSTOMER_AND_OPERATOR" | "OPERATOR";
    registered: boolean;
  }>;
  policyLinks?: Array<{ kind: string; label?: string; url: string }>;
  manualSiteSteps?: Array<{ resolution: "MANUAL_SITE_STEP"; kind: string; code: string; safePath?: string }>;
  procurementHandling: "NORMAL" | "OPERATOR_LATER" | "CUSTOMER_CORRECTION" | "IMPOSSIBLE";
  deliveryOptions: Array<{ id: string; title: string; amountMinor: number; currency: string }>;
  selectedDeliveryOptionRef?: string;
  totals: Array<{ type: string; amountMinor: number; displayText?: string }>;
  authoritativeTotal: Money;
  taxTotal: Money;
  dutiesDisposition: string;
  quoteReadiness: "ESTIMATED" | "CONFIRMED" | "UNSAFE" | "STALE";
  continueUrlSafeRef?: string;
  continueUrlHash?: string;
  quoteFingerprint?: string;
};
export type OrderSheetShippingAddress = {
  recipientName: string;
  addressLine1: string;
  addressLine2?: string;
  city: string;
  region: string;
  postalCode: string;
  country: "US";
  phone?: string;
};
export type OrderSheet = {
  id: string;
  issuedAgencyOrderId?: string;
  sourceCart: { cartId: string; cartVersion: number; snapshotHash: string };
  lines: OrderLine[];
  shippingAddress?: { snapshotRef: string; snapshotRevision: number; snapshotHash: string; maskedSummary: string; country: string };
  shippingAddressInput: OrderSheetShippingAddress;
  shippingAddressSource: "EMPTY" | "ACCOUNT_DEFAULT" | "ORDER_SHEET";
  paymentSelection?: "TVITUSD" | "PAYPAL_SANDBOX" | "PAYPAL_LIVE";
  merchantCheckouts: MerchantCheckout[];
  displayedSnapshotHash?: string;
  passThroughTotal: Money;
  agencyFee: Money;
  customerPayableTotal: Money;
  version: number;
  state: "EDITING" | "DISCOVERING_DELIVERY" | "DELIVERY_SELECTION_REQUIRED" | "PREFLIGHTING" | "READY" | "CONSUMED" | "BLOCKED" | "PRICE_CHANGED" | "RATE_LIMITED" | "EXPIRED";
  blockReason?: string;
  retryAfter?: string;
  expiresAt: string;
};
export type PaymentSelectionAxes = {
  rail: "GIWA" | "PAYPAL";
  providerEnvironment: "TESTNET" | "SANDBOX" | "LIVE";
  asset: "TVITUSD" | "USD";
  economicEffect: "NO_REAL_VALUE" | "REAL_MONEY";
  merchantExecution: "SIMULATED" | "LIVE";
};
export type AgencyOrder = {
  id: string;
  status: "ISSUED";
  lines: OrderLine[];
  shippingAddress: NonNullable<OrderSheet["shippingAddress"]>;
  paymentSelection: PaymentSelectionAxes;
  merchantCheckouts: MerchantCheckout[];
  passThroughTotal: Money;
  agencyFee: { variable: Money; fixed: Money; total: Money; policyVersion: string };
  customerPayableTotal: Money;
  snapshotHash: string;
  issuedAt: string;
  expiresAt: string;
};
// 발행 시 주문 snapshot에 불변 기록되는 결제 축(ADR-0057 2차 P0) —
// economicEffect가 "가짜 돈/진짜 돈"의 주문별 진실이다.
export type AgencyPaymentSelection = {
  rail: "GIWA" | "PAYPAL";
  providerEnvironment: "TESTNET" | "SANDBOX" | "LIVE";
  asset: "TVITUSD" | "USD";
  economicEffect: "NO_REAL_VALUE" | "REAL_MONEY";
  merchantExecution: "SIMULATED" | "LIVE";
};

export type PaymentInstruction = {
  id: string;
  agencyOrderId: string;
  agencyOrderSnapshotHash: string;
  paymentSelection: AgencyPaymentSelection;
  customerPayableTotal: Money;
  paymentPolicyVersion: string;
  state: "PENDING" | "CONSUMED" | "EXPIRED" | "CONFLICT";
  expiresAt: string;
};

type OrderSheetResponse = { schemaVersion: string; orderSheet: OrderSheet };
type AgencyOrderResponse = { schemaVersion: string; agencyOrder: AgencyOrder; paymentInstruction: PaymentInstruction };

export type AgencyOrderProcessStage =
  | "WAITING_CUSTOMER_PAYMENT" | "PAYMENT_RECONCILIATION"
  | "PROCUREMENT_IN_PROGRESS" | "LOGISTICS_IN_PROGRESS"
  | "RESOLUTION_IN_PROGRESS" | "ATTENTION_REQUIRED" | "TERMINAL";

export type AgencyOrderTerminalReason =
  | "COMPLETED_ALL" | "COMPLETED_PARTIAL" | "REFUNDED_ALL" | "CANCELLED" | "EXPIRED";

export type AgencyOrderProcess = {
  agencyOrderId: string;
  state: AgencyOrderProcessStage;
  terminalReason?: AgencyOrderTerminalReason;
  version: number;
  lastReasonCode?: string;
  createdAt: string;
  updatedAt: string;
};

// Procurement 원본 상태의 고객 사영 합성(계약 v7 §12.1, ADR-0052).
export type MerchantOrderUnitSummary = {
	id: string;
  lineId: string;
  unitIndex: number;
  disposition: "PENDING" | "CUSTOMER_REFUND_DUE" | "CUSTOMER_REFUND_SATISFIED" |
    "ZERO_VALUE_SATISFIED" | "NO_PAYMENT_EFFECT";
};

export type MerchantOrderSummary = {
  id: string;
	allocationId: string;
  shopDomain: string;
  merchantId: string;
  checkoutOrdinal: number;
	customerGrossAmount: Money;
  executionMode: "SIMULATED_NO_EFFECT" | "LIVE_MERCHANT_EFFECT";
  state: "PLANNED" | "READY_TO_PLACE" | "PLACEMENT_PENDING" | "PLACED" |
    "PLACEMENT_UNKNOWN" | "FAILED" | "CANCELLED";
  failureCode?: string;
	fundingState?: string;
	cancellationState?: string;
	refundRequestState?: string;
	compensationAction?: string;
	compensationState?: string;
	disputeState?: string;
	disputeOutcome?: string;
  // OrderProcessor의 MO 결정 어휘(ADR-0070 §3.2). 결정 사영 전(발행 직후)에는 비어 있다.
  phase?: MerchantOrderPhase;
  // 고객 취소 intent의 리듀서 판정 — 접수 뒤에도 남아 진행 중·거절·미룸을 보여준다.
  cancelIntent?: MerchantOrderCancelIntent;
  operational: MerchantOrderOperationalProjection;
  units: MerchantOrderUnitSummary[];
  updatedAt: string;
};

export type MerchantOrderPhase =
  | "PLANNED" | "FUNDING" | "PURCHASING" | "PLACED" | "FULFILLING" | "DELIVERED"
  | "COMPENSATING" | "COMPENSATED" | "FAILED" | "CANCELLED";

export type MerchantOrderCancelIntent = {
  kind: "PRE_EFFECT" | "DELAY_RULE";
  outcome: "EFFECT_ISSUED" | "DEFERRED" | "REJECTED" | "SUCCEEDED" | "SUPERSEDED";
  code?: string;
};

export type MerchantOrderOperationalStage =
  | "PROCUREMENT_PENDING" | "PROCUREMENT_ACTIVE" | "AWAITING_SHIPMENT" | "IN_TRANSIT"
  | "DELIVERY_EXCEPTION" | "REFUND_REVIEW" | "RETURN_IN_PROGRESS"
  | "COMPENSATION_PENDING" | "PAYPAL_DISPUTE" | "ATTENTION_REQUIRED" | "DELIVERED" | "REFUNDED"
  | "PROCUREMENT_FAILED" | "CANCELLED";
export type MerchantOrderWorkStage = "PROCUREMENT" | "LOGISTICS" | "ISSUE" | "DONE";
export type MerchantOrderProgressState = "WAITING" | "CURRENT" | "DONE" | "ISSUE";
export type MerchantOrderOperationalProjection = {
  stage: MerchantOrderOperationalStage;
  workStage: MerchantOrderWorkStage;
  terminalReason?: "DELIVERED" | "REFUNDED" | "PROCUREMENT_FAILED" | "CANCELLED";
  progress: {
    funding: MerchantOrderProgressState;
    procurement: MerchantOrderProgressState;
    delivery: MerchantOrderProgressState;
    resolution: MerchantOrderProgressState;
  };
  units: {
    total: number;
    ordered: number;
    procuring: number;
    awaitingShipment: number;
    inTransit: number;
    delivered: number;
    exception: number;
    returnInProgress: number;
    refundRequested: number;
    refundPending: number;
    refunded: number;
    procurementFailed: number;
    cancelled: number;
  };
  resolutionCause?: string;
  resolutionDecision?: string;
  returnState?: string;
  compensationAction?: string;
  compensationState?: string;
  disputeState?: string;
  disputeOutcome?: string;
};

// Logistics 원본 상태의 고객 사영 합성(계약 v7 §9). 실물 패키지 단위이며,
// units.disposition 자리에는 unit fulfillment가 담긴다.
export type ShipmentSummary = {
  id: string;
  merchantOrderId: string;
  carrier: string;
  trackingRef: string;
  state: "CREATED" | "LABEL_CREATED" | "IN_TRANSIT" | "OUT_FOR_DELIVERY" | "DELIVERED" |
    "EXCEPTION" | "LOST" | "RETURN_TO_SENDER" | "RETURNED" | "CANCELLED_NO_EFFECT" |
    "EXCEPTION_RECONCILIATION";
  units: Array<Omit<MerchantOrderUnitSummary, "disposition"> & { disposition: string }>;
  updatedAt: string;
};

// 주문 스코프 고지함(ADR-0052 §2.6 — 회신 없음).
export type CustomerNotice = {
  id: string;
  agencyOrderId: string;
  kind: "SYSTEM" | "OPERATOR";
  body: string;
  createdAt: string;
  readAt?: string;
};

export type AgencyOrderReceipt = {
  id: string;
  agencyOrderId: string;
  settlementPaymentId?: string;
  customerPaymentId?: string;
  kind: "TEST" | "LIVE_ORDER_RECORD";
  paymentRail: "GIWA" | "PAYPAL";
  providerEnvironment: "TESTNET" | "SANDBOX" | "LIVE";
  asset: "TVITUSD" | "USD";
  economicEffect: "NO_REAL_VALUE" | "REAL_MONEY";
  merchantExecutionMode: "SIMULATED_NO_EFFECT" | "LIVE_MERCHANT_EFFECT";
  executionProfileHash: `0x${string}`;
  // A Live value means at least one merchant order reached PLACED. This
  // Vitlane record still does not replace the merchant's tax/legal receipt.
  legalSale: boolean;
  // 신규 발급분은 terminalReason vocabulary, 과거 발급분은 legacy 값이다(불변 증거).
  terminalState: "COMPLETED_ALL" | "COMPLETED_PARTIAL" | "REFUNDED_ALL" | "COMPLETED" | "REFUNDED";
  // GIWA는 chain tx hash, PayPal은 Capture/Refund ID다.
  terminalTxHash: string;
  receiptHash: `0x${string}`;
  payload: unknown;
  createdAt: string;
};

export type RefundRequest = {
  id: string;
  agencyOrderId: string;
	merchantOrderId: string;
	allocationId: string;
	requestedGrossAmount: Money;
  state: "REQUESTED" | "REVIEWING" | "RESOLVED";
  reasonCode: string;
  publicRationale: string;
	decision?: "APPROVED" | "REJECTED";
	decisionPublicRationale?: string;
	decidedAt?: string;
  createdAt: string;
  updatedAt: string;
};

// CustomerAction은 서버가 계산한 고객 명령 공간이다(ADR-0055 §5). Web은 자격
// 판정을 소유하지 않고 이 공간을 렌더한다 — 권위 검증은 각 명령 endpoint가
// 트랜잭션에서 재수행한다.
export type CustomerActionKind = "PAY" | "CANCEL_PRE_EFFECT" | "CANCEL_DELAY_RULE" | "REQUEST_REFUND";
export type CustomerAction = {
  kind: CustomerActionKind;
  rail?: "GIWA" | "PAYPAL";
	eligibleMerchantOrderIds?: string[];
  reasonCodes?: RefundReasonCode[];
};
export function customerActionOf(projection: AgencyOrderProjection, kind: CustomerActionKind) {
  return (projection.availableActions ?? []).find((action) => action.kind === kind);
}

// UnitStage is a physical-unit display projection; refund money remains on MO.
// 소스(agencyorder/domain.DeriveUnitStage)이며 FE는 라벨 사전만 소유한다 —
// 어휘 동기는 골든 fixture(agency-order-units.v2.json) 계약 테스트가 지킨다.
export type UnitStage =
  | "ORDERED" | "PROCURING" | "PROCUREMENT_FAILED" | "CANCELLED"
  | "AWAITING_SHIPMENT" | "IN_TRANSIT" | "DELIVERED" | "EXCEPTION"
  | "RETURN_IN_PROGRESS" | "REFUND_REQUESTED" | "REFUND_PENDING" | "REFUNDED";

export type AgencyOrderUnitView = {
	merchantOrderUnitId: string;
	merchantOrderId: string;
	allocationId: string;
  lineId: string;
  unitIndex: number;
  shopDomain: string;
  stage: UnitStage;
	refundStatus: "AVAILABLE" | "REQUESTED" | "REFUND_PENDING" | "REFUNDED";
  shipment?: { id: string; carrier: string; trackingRef: string; state: string };
  returnState?: string;
  resolutionCause?: string;
  resolutionDecision?: string;
};

export type AgencyOrderProjection = {
  agencyOrder: AgencyOrder;
  paymentInstruction: PaymentInstruction;
  process: AgencyOrderProcess;
  availableActions: CustomerAction[];
  payment?: AgencyOrderPaymentProjection;
  chainTransactions: AgencyOrderChainTransaction[];
  merchantOrders: MerchantOrderSummary[];
  shipments: ShipmentSummary[];
  units: AgencyOrderUnitView[];
  refundRequests?: RefundRequest[];
  notices?: CustomerNotice[];
  receipt?: AgencyOrderReceipt;
};

export type AgencyOrderPaymentProjection = Pick<
  SettlementPayment,
  "id" | "amountBaseUnits" | "payTxHash" | "completeTxHash" |
  "refundTxHash" | "safeBlock" | "finalizedBlock" | "lastReasonCode" | "updatedAt"
> & {
  // GIWA는 SettlementPayment 상태, PAYPAL은 CustomerPayment 상태를 담는다.
  state: string;
  rail?: "GIWA" | "PAYPAL";
  paypalOrderId?: string;
  captureId?: string;
  paypalRefundId?: string;
  // 주문 국면(완료/환불)은 process.state/terminalReason이 소유한다(ADR-0052).
  refundedTotalCent?: number;
};

export type AgencyOrderChainTransaction = {
  purpose: "CLAIM" | "APPROVE" | "PAY" | "COMPLETE" | "REFUND";
  txHash: `0x${string}`;
  state: "SUBMITTED" | "SAFE" | "FINALIZED" | "FAILED" | "REORGED";
  blockNumber: number;
  updatedAt: string;
};

export type AgencyOrderListView =
  | "PAYMENT_REQUIRED" | "IN_PROGRESS" | "NEEDS_ATTENTION" | "FINISHED" | "ALL";
export type AgencyOrderListSort = "UPDATED_DESC" | "CREATED_DESC" | "CREATED_ASC";
export type AgencyOrderListCounts = Record<AgencyOrderListView, number>;
export type AgencyOrderListResponse = {
  schemaVersion: string;
  agencyOrders: AgencyOrderProjection[];
  countsByView: AgencyOrderListCounts;
  nextCursor?: string;
};

export type AgencyOrderCapability = {
  state: "READY" | "UNAVAILABLE";
  reasonCode?: string;
  checkoutProvider: "SHOPIFY" | "STUB" | "NONE";
  capabilityRevision: number;
  paymentRails: {
    tvitusd: AgencyOrderPaymentRailCapability;
    paypalSandbox: AgencyOrderPaymentRailCapability;
    paypalLive: AgencyOrderPaymentRailCapability;
  };
};

export type AgencyOrderPaymentRailCapability = {
      state: "READY" | "PAUSED" | "UNAVAILABLE";
      orderIssueState: "READY" | "PAUSED" | "UNAVAILABLE";
      paymentInitiationState: "READY" | "PAUSED" | "UNAVAILABLE";
      paymentMethod: "TVITUSD" | "PAYPAL_SANDBOX" | "PAYPAL_LIVE";
      providerEnvironment: "TESTNET" | "SANDBOX" | "LIVE";
      asset: "TVITUSD" | "USD";
      economicEffect: "NO_REAL_VALUE" | "REAL_MONEY";
};

export async function getAgencyOrderCapability() {
  return request<{
    schemaVersion: "vitlane.agency-order-capability.v2";
    capability: AgencyOrderCapability;
  }>("/api/v1/agency-order-capability");
}

export async function createOrderSheet(curationId: string, expectedCartVersion: number, idempotencyKey: string) {
  return request<OrderSheetResponse>(`/api/v1/curations/${encodeURIComponent(curationId)}/order-sheets`, {
    method: "POST",
    body: JSON.stringify({ expectedCartVersion, idempotencyKey }),
  });
}

export async function getOrderSheet(id: string) {
  return request<OrderSheetResponse>(`/api/v1/order-sheets/${encodeURIComponent(id)}`);
}

export async function setOrderSheetShippingAddress(
  id: string,
  expectedVersion: number,
  address: OrderSheetShippingAddress,
) {
  return request<OrderSheetResponse>(
    `/api/v1/order-sheets/${encodeURIComponent(id)}/shipping-address`,
    {
      method: "PUT",
      body: JSON.stringify({ expectedVersion, ...address }),
    },
  );
}

export async function preflightOrderSheet(
  id: string,
  expectedVersion: number,
  paymentMethod: "TVITUSD" | "PAYPAL_SANDBOX" | "PAYPAL_LIVE",
) {
  return request<OrderSheetResponse>(`/api/v1/order-sheets/${encodeURIComponent(id)}/preflight`, {
    method: "POST",
    body: JSON.stringify({ expectedVersion, paymentMethod }),
  });
}

export async function selectDeliveryOptions(
  id: string,
  expectedVersion: number,
  selections: Array<{ shopDomain: string; groupId: string; optionId: string }>,
) {
  return request<OrderSheetResponse>(
    `/api/v1/order-sheets/${encodeURIComponent(id)}/delivery-selections`,
    {
      method: "PUT",
      body: JSON.stringify({ expectedVersion, selections }),
    },
  );
}

export type ProcurementApproval = {
  orderMessage?: string;
  deliveryMessage?: string;
  agencyConsent: boolean;
  privacyConsent: boolean;
  locale: "en-US" | "ko-KR";
  copyVersion: "procurement-authorization.v1";
};

export async function issueAgencyOrder(
  id: string,
  expectedVersion: number,
  displayedSnapshotHash: string,
  idempotencyKey: string,
  expectedCapabilityRevision: number,
  procurementApproval: ProcurementApproval,
) {
  return request<AgencyOrderResponse>(`/api/v1/order-sheets/${encodeURIComponent(id)}/issue`, {
    method: "POST",
    body: JSON.stringify({ expectedVersion, expectedCapabilityRevision, displayedSnapshotHash, idempotencyKey, procurementApproval }),
  });
}

export async function getAgencyOrder(id: string) {
  return request<{ schemaVersion: string; agencyOrder: AgencyOrderProjection }>(
    `/api/v1/agencyOrder/${encodeURIComponent(id)}`,
  );
}

export async function listAgencyOrders(query: {
  view?: AgencyOrderListView;
  sort?: AgencyOrderListSort;
  limit?: number;
  cursor?: string;
} = {}) {
  const agencyOrderListPath = "/api/v1/agencyOrder";
  const parameters = new URLSearchParams();
  if (query.view) parameters.set("view", query.view);
  if (query.sort) parameters.set("sort", query.sort);
  if (query.limit) parameters.set("limit", String(query.limit));
  if (query.cursor) parameters.set("cursor", query.cursor);
  const suffix = parameters.size ? `?${parameters.toString()}` : "";
  return request<AgencyOrderListResponse>(`${agencyOrderListPath}${suffix}`);
}

export async function revealAgencyOrderShipping(id: string) {
  return request<{
    address: {
      recipientName: string;
      addressLine1: string;
      addressLine2?: string;
      city: string;
      region: string;
      postalCode: string;
      country: string;
      phone?: string;
    };
  }>(`/api/v1/agencyOrder/${encodeURIComponent(id)}/shipping-address-reveal`, {
    method: "POST",
    body: "{}",
  });
}

export async function authorizeAgencyOrder(
  id: string,
  walletId: string,
  ownershipProofId: string,
) {
  return request<AuthorizationRecord | {schemaVersion: "vitlane.payment-instruction-confirmation.v1"; outcome: "WAITING"; reasonCode: string}>(
    `/api/v1/agencyOrder/${encodeURIComponent(id)}/settlement-authorizations`,
    {
      method: "POST",
      body: JSON.stringify({ walletId, ownershipProofId }),
    },
  );
}

export async function submitAgencyOrderWalletTransaction(
  id: string,
  purpose: "CLAIM" | "APPROVE",
  txHash: string,
) {
  return request<void>(
    `/api/v1/agencyOrder/${encodeURIComponent(id)}/wallet-transactions`,
    { method: "POST", body: JSON.stringify({ purpose, txHash }) },
  );
}

export async function submitAgencyOrderPayTransaction(id: string, txHash: string) {
  return request<void>(
    `/api/v1/agencyOrder/${encodeURIComponent(id)}/settlement-transactions`,
    { method: "POST", body: JSON.stringify({ txHash }) },
  );
}

export async function getAgencyOrderSettlement(id: string) {
	return request<{
		payment: SettlementPayment;
		authorization: AuthorizationRecord;
	}>(
    `/api/v1/agencyOrder/${encodeURIComponent(id)}/settlement`,
  );
}

export async function getAgencyOrderReceipt(id: string) {
  return request<{ receipt: AgencyOrderReceipt }>(
    `/api/v1/agencyOrder/${encodeURIComponent(id)}/receipt`,
  );
}

// --- PayPal Sandbox rail (Phase 8 Step 3, ADR-0050) ---

export type PayPalCustomerPayment = {
  id: string;
  agencyOrderId: string;
  rail: "PAYPAL";
  providerEnvironment: "SANDBOX" | "LIVE";
  asset: "USD";
  economicEffect: "NO_REAL_VALUE" | "REAL_MONEY";
  amountMinor: number;
  currency: "USD";
  state: "CREATED" | "ACTION_REQUIRED" | "PROCESSING" | "OUTCOME_UNKNOWN" |
		"AUTHORIZED" | "PARTIALLY_CAPTURED" | "CAPTURED" | "CLOSED" |
		"FAILED" | "ABANDONED" | "EXPIRED" | "SUPERSEDED";
  lastReasonCode?: string;
  updatedAt: string;
};

export type PayPalCheckout = {
  schemaVersion: "vitlane.payment-paypal-checkout.v1";
  payment: PayPalCustomerPayment;
  attempt: {
    id: string;
    sequence: number;
    state: string;
    paypalOrderId?: string;
    lastReasonCode?: string;
  };
  approvalUrl?: string;
  returnNonce?: string;
};

export async function startPayPalCheckout(agencyOrderId: string) {
  return request<PayPalCheckout>(
    `/api/v1/agencyOrder/${encodeURIComponent(agencyOrderId)}/paypal/checkout`,
    { method: "POST", body: "{}" },
  );
}

export async function resumePayPalCheckout(
  agencyOrderId: string,
  returnNonce: string,
  cancelled: boolean,
) {
  return request<PayPalCheckout>(
    `/api/v1/agencyOrder/${encodeURIComponent(agencyOrderId)}/paypal/resume`,
    { method: "POST", body: JSON.stringify({ returnNonce, cancelled }) },
  );
}

export async function getPayPalCheckout(agencyOrderId: string) {
  return request<PayPalCheckout>(
    `/api/v1/agencyOrder/${encodeURIComponent(agencyOrderId)}/paypal`,
  );
}

// 단순변심 OFF 게이트(ADR-0052 §1): 과실 계열 typed 사유만 접수된다.
export type RefundReasonCode =
  "ITEM_NOT_RECEIVED" | "ITEM_DAMAGED_DEFECTIVE" | "WRONG_ITEM_RECEIVED" |
  "ORDER_DELAYED" | "OTHER_SERVICE_FAULT";

export async function requestAgencyOrderRefund(
  agencyOrderId: string,
	merchantOrderId: string,
  reasonCode: RefundReasonCode,
  publicRationale: string,
) {
  return submitProcessAction(
    `/api/v1/agencyOrder/${encodeURIComponent(agencyOrderId)}/refund-requests`,
		{ method: "POST", body: JSON.stringify({ merchantOrderId, reasonCode, publicRationale }) },
  );
}

// 종전 주문 고지함 고객 API(listNotices·markNoticeRead)는 ADR-0059로 Support
// 대화에 흡수되어 제거됐다. SYSTEM 지연 rule 고지는 projection.notices로만
// 내려간다.

// 결제 후·첫 merchant effect 전 자유 취소(ADR-0052 §2.4). 신 고지 이후 발행
// 주문은 FEE_RETAINED(수수료 수취·실비 환불), 구주문은 GROSS다.
export async function cancelAgencyOrder(agencyOrderId: string, merchantOrderId: string) {
  // ADR-0056: 취소는 비동기 intent 접수다 — 실행·경합 판정은 서버 process가
  // 수행하고 결과는 주문 notice·상태 갱신으로 전달된다.
  return submitProcessAction(
    `/api/v1/agencyOrder/${encodeURIComponent(agencyOrderId)}/cancellation`,
		{ method: "POST", body: JSON.stringify({ merchantOrderId }) },
  );
}

// 30일 지연 rule 무료 취소(FTC, ADR-0052 §2.5) — 발행 후 30일 미배송 시
// 미수령 상품 전액(GROSS) 환불.
export async function cancelAgencyOrderDelayRule(agencyOrderId: string, merchantOrderId: string) {
  return submitProcessAction(
    `/api/v1/agencyOrder/${encodeURIComponent(agencyOrderId)}/delay-cancellation`,
		{ method: "POST", body: JSON.stringify({ merchantOrderId }) },
  );
}
