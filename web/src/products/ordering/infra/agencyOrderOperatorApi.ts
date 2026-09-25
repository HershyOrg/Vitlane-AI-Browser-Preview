import { applyOwnerAction, submitProcessAction, type ProcessReceipt } from "./orderProcessApi";
import { request } from "../../../shared/api/client";
import type {
  AgencyOrder,
  AgencyOrderChainTransaction,
  AgencyOrderPaymentProjection,
  AgencyOrderProcess,
  AgencyOrderUnitView,
  MerchantOrderSummary,
  MerchantOrderOperationalProjection,
  Money,
  ShipmentSummary,
} from "./agencyOrderApi";

// Procurement 운영자 work surface(계약 v7 §14, ADR-0052) — 실행 단위는
// MerchantOrder(Shop-checkout) Task이고 창구는 단일 ManualOperatorExecutor다.

export type ProcurementExecutionMode = "SIMULATED_NO_EFFECT" | "LIVE_MERCHANT_EFFECT";
export type ProcurementPlacementEvidenceKind =
  | "SANDBOX_TEST_EVIDENCE"
  | "LIVE_MERCHANT_EFFECT_EVIDENCE";
export type ProcurementPlacementEvidence = {
  kind: ProcurementPlacementEvidenceKind;
  externalOrderRef: string;
  receiptSafeRef: string;
  actualAmountMinor: number;
  currency: "USD";
  evidenceSource: "OPERATOR_OBSERVATION" | "MERCHANT_PAGE" | "MERCHANT_POLICY" | "RECEIPT" | "OTHER";
  evidenceHash: string;
  observedAt: string;
  recordedByUserId?: string;
  recordedAt?: string;
  claimsExternalLiveEffect: boolean;
};
export type ProcurementPlacementEvidenceInput = Omit<
  ProcurementPlacementEvidence,
  "kind" | "currency" | "actualAmountMinor" | "evidenceHash" | "observedAt" | "recordedByUserId" | "recordedAt" | "claimsExternalLiveEffect"
> & {
  evidenceKind: ProcurementPlacementEvidenceKind;
  amountMode: "UNCHANGED" | "CHANGED";
  actualAmountMinor?: number;
};

export type ProcurementTask = {
  id: string;
  merchantOrderId: string;
  agencyOrderId: string;
  state: "QUEUED" | "CLAIMED" | "IN_PROGRESS" | "SUCCEEDED" | "FAILED" | "CANCELLED" | "OUTCOME_UNKNOWN";
  assignedOperatorUserId?: string;
  assignedAt?: string;
  leaseUntil?: string;
  handledAt?: string;
  updatedAt: string;
};

export type ProcurementMerchantOrder = {
  id: string;
  agencyOrderId: string;
  merchantId: string;
  shopDomain: string;
  checkoutOrdinal: number;
  // 발행 시 고정된 checkout 스냅샷 원문 — 정보 계약(§4.3)의 권위 소스다.
  checkoutSnapshot: {
    authoritativeTotal?: { amountMinor: number; currency: string };
    taxTotal?: { amountMinor: number; currency: string };
    deliveryGroups?: Array<{ id: string; selectedOptionRef?: string; options: Array<{ id: string; title: string; amountMinor: number; currency: string }> }>;
    providerStatus?: string;
    quoteReadiness?: "ESTIMATED" | "CONFIRMED" | "UNSAFE" | "STALE";
    procurementHandling?: "NORMAL" | "OPERATOR_LATER" | "CUSTOMER_CORRECTION" | "IMPOSSIBLE";
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
    manualSiteSteps?: Array<{ resolution: "MANUAL_SITE_STEP"; kind: string; code: string; safePath?: string }>;
    expiresAt?: string;
    continueUrlSafeRef?: string;
  };
  executionMode: ProcurementExecutionMode;
  state: "PLANNED" | "READY_TO_PLACE" | "PLACEMENT_PENDING" | "PLACED" | "PLACEMENT_UNKNOWN" | "FAILED" | "CANCELLED";
  failureCode?: string;
  externalOrderRef?: string;
  placementEvidence?: ProcurementPlacementEvidence;
};

export type ProcurementUnit = {
  id: string;
  lineId: string;
  unitIndex: number;
  disposition: string;
};

export type ProcurementQueueItem = {
  task: ProcurementTask;
  merchantOrder: ProcurementMerchantOrder;
  units: ProcurementUnit[];
  agencyOrder: AgencyOrder;
  processState: string;
  terminalReason?: string;
  // 이 MO의 배송 진실 요약(운영정합 4차 §3) — delivered는 수령 확인만 세고,
  // 예외 lane(누락·오배송·분실) 수는 exceptionUnits로 따로 온다(5차 C3).
  logisticsSummary: {
    expectedUnits: number;
    awaitingUnits: number;
    inTransitUnits: number;
    deliveredUnits: number;
    exceptionUnits: number;
    returnInProgressUnits: number;
  };
  // 선택 배송 옵션의 서버 파생(5차 C2) — 해석 실패는 derived=false로 명시된다.
  delivery: { derived: boolean; title?: string; amountMinor: number };
	// Procurement seller-effect gate. UNKNOWN/PENDING remains visible and may
	// only be reconciled through the existing MO activation operation.
	funding: { positionId?: string; state?: string; amountMinor: number; rail?: string };
	refundRequestState?: string;
	cancellationState?: string;
	resolutionCause?: string;
	resolutionDecision?: string;
	returnState?: string;
	compensationAction?: string;
	compensationState?: string;
	disputeState?: string;
	disputeOutcome?: string;
	operational: MerchantOrderOperationalProjection;
	assignmentState: "UNASSIGNED" | "ACTIVE" | "EXPIRED" | "COMPLETED";
};

export type OperatorShippingAddress = {
  recipientName: string;
  addressLine1: string;
  addressLine2?: string;
  city: string;
  region: string;
  postalCode: string;
  country: string;
  phone?: string;
};

export async function listProcurementQueue() {
  return request<{ schemaVersion: string; items: Array<Omit<ProcurementQueueItem, "operational" | "assignmentState">> }>(
    "/api/v1/admin/procurement/queue",
  );
}

// OperatorWorkItem은 결제 대사·process 개입·조달 실행·환불 심사·배송 예외 판정·
// 회수 진행)의 통일 계약이다(ADR-0055 §5·ADR-0056 §3). detail은 kind별 owner
// 사영 원문이고 actions는 이 상태에서 허용되는 다음 행동의 이름이다 — 화면은
// actions에 있는 행동만 연다(자문 공간 — 권위는 owner endpoint).
export type OperatorWorkItemKind =
  | "PAYMENT_RECONCILIATION" | "PROCESS_INTERVENTION" | "PROCUREMENT_EXECUTION" | "REFUND_REVIEW"
  | "DELIVERY_RESOLUTION" | "RETURN_PROGRESS";
export type PaymentReconciliationItem = {
  paymentId: string;
  agencyOrderId: string;
  paypalAttemptId: string;
  paypalOrderId?: string;
  providerEnvironment: "SANDBOX" | "LIVE";
  amountMinor: number;
  currency: string;
	paymentState: "OUTCOME_UNKNOWN";
  attemptState: string;
  reasonCode: string;
  updatedAt: string;
};
export type PayPalResourceAdoptionKind = "PAYPAL_REAUTHORIZATION" | "PAYPAL_MO_REFUND";
export type PayPalResourceAdoptionItem = {
  reconciliationKind: PayPalResourceAdoptionKind;
  operationId: string;
  agencyOrderId: string;
  merchantOrderId: string;
  compensationId?: string;
  providerEnvironment: "SANDBOX" | "LIVE";
  amountMinor: number;
  currency: "USD";
  operationState: "SENT" | "UNKNOWN";
  ownerState: string;
  reasonCode?: string;
  firstSentAt: string;
  idempotencyDeadline: string;
  updatedAt: string;
};
export type PayPalReauthorizationAdoptionInput = {
  providerAuthorizationId: string;
  evidenceSource: "PAYPAL_DASHBOARD" | "PAYPAL_SUPPORT" | "OTHER";
  evidenceHash: string;
  internalNote?: string;
  observedAt: string;
};
export type PayPalMORefundAdoptionInput = {
  providerRefundId: string;
  publicRationale: string;
  evidenceSource: "PAYPAL_DASHBOARD" | "PAYPAL_API" | "PAYPAL_WEBHOOK" | "OTHER";
  evidenceHash: string;
  observedAt: string;
};
export type ProcessInterventionItem = {
  merchantOrderId?: string;
  guidance: { reasonCode?: string; waitingFor?: string; customerAction: string; operatorAction: string };
  effectId: string;
  agencyOrderId: string;
  target: string;
  type: string;
  lastErrorCode?: string;
  attemptCount: number;
  updatedAt: string;
};
export type AccountingEnvironment = "SANDBOX" | "LIVE" | "TESTNET";
export type AccountingEventKind =
  | "CUSTOMER_CASH_IN"
  | "MERCHANT_PURCHASE"
  | "CUSTOMER_COMPENSATION"
  | "AUTHORIZATION_RELEASE"
  | "MERCHANT_RECOVERY";
export type AccountingDirection = "CREDIT" | "DEBIT" | "NEUTRAL";

export type OrderAccountingEvent = {
  id: string;
  kind: AccountingEventKind;
  direction: AccountingDirection;
  merchantOrderId?: string;
  allocationId?: string;
  amountMinor: number;
  customerGrossMinor?: number;
  processorFeeMinor?: number;
  economicsReconciled: boolean;
  source: string;
  cause?: string;
  occurredAt: string;
};

export type MerchantOrderAccounting = {
  allocationId: string;
  merchantOrderId?: string;
  shopDomain: string;
  checkoutOrdinal: number;
  passThroughMinor: number;
  feeVariableMinor: number;
  feeFixedMinor: number;
  feeTotalMinor: number;
  customerGrossMinor: number;
  feePolicyVersion: string;
  fundingState: string;
  merchantOrderState?: string;
  merchantPaymentState?: string;
  compensationAction?: string;
  compensationState?: string;
  compensationCause?: string;
  exceptionCause?: string;
  actualCustomerGrossInMinor: number;
  actualProcessorFeeMinor: number;
  actualNetCashInMinor: number;
  actualMerchantSpendMinor: number;
  actualCustomerCompensatedMinor: number;
  actualMerchantRecoveredMinor: number;
  unreconciledCashGrossMinor: number;
  realizedBalanceMinor: number;
  forecastNetCashInMinor: number;
  forecastProcessorFeeMinor: number;
  forecastMerchantSpendMinor: number;
  expectedCompensationMinor: number;
  forecastAdjustmentMinor: number;
  forecastBalanceMinor: number;
  attentionReasons: string[];
};

export type OrderAccountingProjection = {
  agencyOrderId: string;
  customerPaymentId: string;
  rail: "PAYPAL" | "GIWA";
  providerEnvironment: AccountingEnvironment;
  paymentState: string;
  currency: "USD";
  actualCustomerGrossInMinor: number;
  actualProcessorFeeMinor: number;
  actualNetCashInMinor: number;
  actualMerchantSpendMinor: number;
  actualCustomerCompensatedMinor: number;
  actualMerchantRecoveredMinor: number;
  unreconciledCashGrossMinor: number;
  realizedBalanceMinor: number;
  forecastNetCashInMinor: number;
  forecastProcessorFeeMinor: number;
  forecastMerchantSpendMinor: number;
  expectedCompensationMinor: number;
  forecastAdjustmentMinor: number;
  forecastBalanceMinor: number;
  requiresAttention: boolean;
  attentionReasons: string[];
  merchantOrders: MerchantOrderAccounting[];
  events: OrderAccountingEvent[];
  createdAt: string;
};

type OperatorWorkItemCore =
  | { kind: "PAYMENT_RECONCILIATION"; id: string; agencyOrderId: string; state: string; updatedAt: string; actions: string[]; detail: PaymentReconciliationItem | PayPalResourceAdoptionItem }
  | { kind: "PROCESS_INTERVENTION"; id: string; agencyOrderId: string; state: string; updatedAt: string; actions: string[]; detail: ProcessInterventionItem }
  | { kind: "PROCUREMENT_EXECUTION"; id: string; agencyOrderId: string; state: string; assignedOperatorUserId?: string; assignmentState: ProcurementQueueItem["assignmentState"]; operational: MerchantOrderOperationalProjection; updatedAt: string; actions: string[]; detail: Omit<ProcurementQueueItem, "operational" | "assignmentState"> }
  | { kind: "REFUND_REVIEW"; id: string; agencyOrderId: string; state: string; updatedAt: string; actions: string[]; detail: OperatorRefundRequest }
  | { kind: "DELIVERY_RESOLUTION"; id: string; agencyOrderId: string; state: string; updatedAt: string; actions: string[]; detail: OperatorExpectedUnit }
  | { kind: "RETURN_PROGRESS"; id: string; agencyOrderId: string; state: string; updatedAt: string; actions: string[]; detail: OperatorReturn };
export type OperatorWorkItem = OperatorWorkItemCore & { accounting?: OrderAccountingProjection };
export type OperatorWorkItemCounts = Record<OperatorWorkItemKind, number>;

export type OrderLookupIdentifierType =
  | "AUTO" | "AGENCY_ORDER_ID" | "PAYMENT_ID" | "PAYPAL_ORDER_ID"
  | "PAYPAL_CAPTURE_ID" | "PAYPAL_REFUND_ID" | "MERCHANT_ORDER_ID"
  | "MERCHANT_ORDER_REF" | "SHIPMENT_ID" | "TRACKING_REF" | "GIWA_TX_HASH";
export type OrderLookupEnvironment = "ANY" | "SANDBOX" | "TESTNET" | "LIVE";
export type OrderLookupInput = {
  identifierType: OrderLookupIdentifierType;
  value: string;
  environment: OrderLookupEnvironment;
  shopDomain?: string;
  carrier?: string;
};
export type OrderLookupMatch = {
  agencyOrderId: string;
  matchedBy: Exclude<OrderLookupIdentifierType, "AUTO">;
  environment: Exclude<OrderLookupEnvironment, "ANY">;
  paymentRail: "GIWA" | "PAYPAL";
};
export type OrderIdentifier = {
  kind: Exclude<OrderLookupIdentifierType, "AUTO">;
  value: string;
  qualifier?: string;
  relatedResourceId?: string;
};
export type OrderEvidenceLine = {
  lineId: string;
  productTitle: string;
  variantTitle?: string;
  quantity: number;
  shopDomain: string;
  observedAt: string;
  evidenceHash: string;
};
export type OrderEvidenceSummary = {
  agencyOrderId: string;
  status: string;
  issuedAt: string;
  expiresAt: string;
  snapshotHash: string;
  executionProfile: {
    paymentRail: "GIWA" | "PAYPAL";
    providerEnvironment: "SANDBOX" | "TESTNET" | "LIVE";
    asset: "USD" | "TVITUSD";
    economicEffect: "NO_REAL_VALUE" | "REAL_MONEY";
    merchantExecutionMode: "SIMULATED_NO_EFFECT" | "LIVE_MERCHANT_EFFECT";
  };
  executionProfileHash: string;
  customerPayableTotal: Money;
  passThroughTotal: Money;
  agencyFeeTotal: Money;
  shippingMasked: string;
  shippingCountry: string;
  issuanceEvidence: {
    orderSheetSessionId: string;
    displayedSnapshotHash: string;
    disclosureVersion: string;
    idempotencyKeyHash: string;
  };
  lines: OrderEvidenceLine[];
};
export type OrderInvestigation = {
  order: OrderEvidenceSummary;
  process: AgencyOrderProcess;
  paymentInstruction: {
    id: string;
    state: string;
    agencyOrderSnapshotHash: string;
    executionProfileHash: string;
    customerPayableTotal: Money;
    paymentPolicyVersion: string;
    idempotencyKeyHash: string;
    createdAt: string;
    expiresAt: string;
  };
  payment?: AgencyOrderPaymentProjection;
  identifiers: OrderIdentifier[];
  chainTransactions: AgencyOrderChainTransaction[];
  merchantOrders: MerchantOrderSummary[];
  shipments: ShipmentSummary[];
  units: AgencyOrderUnitView[];
  receipt?: {
    id: string;
    kind: string;
    paymentRail: string;
    providerEnvironment: string;
    economicEffect: string;
    merchantExecutionMode: string;
    executionProfileHash: string;
    terminalState: string;
    terminalTxHash?: string;
    receiptHash: string;
    createdAt: string;
  };
  checkpoints: Array<{
    kind: string;
    state: string;
    reasonCode?: string;
    reference?: string;
    observedAt: string;
  }>;
};

export async function lookupOperatorOrder(input: OrderLookupInput) {
  return request<{ schemaVersion: string; match: OrderLookupMatch }>(
    "/api/v1/admin/ordering/order-lookups",
    { method: "POST", body: JSON.stringify(input) },
  );
}

// ADR-0070 §4.7 — 결정 원장 ⋈ 이벤트 ⋈ 커맨드 timeline(운영자 조사 화면의
// 요청 시 열람 — 자동 폴링 없음).
export type OrderTimelineDecision = {
  version: number;
  seqFrom: number;
  seqTo: number;
  stageBefore?: string;
  stageAfter: string;
  terminalReason?: string;
  lastReasonCode?: string;
  stageChanged: boolean;
  merchantOrders: Array<{
    merchantOrderId: string;
    phaseBefore?: string;
    phaseAfter: string;
    intentBefore?: string;
    intentAfter?: string;
    attentionBefore?: string;
    attentionAfter?: string;
    reason?: string;
  }>;
  effects: Array<{ target: string; type: string; idempotencyKey: string }>;
  wakeAt?: string;
  processState: Record<string, unknown>;
  decidedAt: string;
};

export type OrderTimelineEvent = {
 flowId?: string;
 causationEffectId?: string;
 sourceEntityVersion?: number;
  id: number;
  seq: number;
  source: string;
  type: string;
  payload: Record<string, unknown>;
  occurredAt: string;
  recordedAt: string;
  appliedVersion?: number;
};

export type OrderTimelineEffect = {
 effectId: string; flowId: string; requestId?: string; merchantOrderId?: string;
 target: string; type: string; deliveryState: "PENDING" | "CONSUMED";
 claimVersion: number; idempotencyKey: string; causedByEventId: number;
 attemptCount: number; nextAttemptAt: string; payload: Record<string, unknown>;
 createdAt: string; consumedAt?: string;
};

export type OrderTimeline = {
 requests: ProcessReceipt[];
  agencyOrderId: string;
  process: {
    state: string;
    terminalReason?: string;
    lastReasonCode?: string;
    version: number;
    lastAppliedSeq: number;
    wakeAt?: string;
    updatedAt: string;
  };
  decisions: OrderTimelineDecision[];
  events: OrderTimelineEvent[];
  effects: OrderTimelineEffect[];
};

export async function getOperatorOrderTimeline(agencyOrderId: string) {
  return request<{ schemaVersion: string; timeline: OrderTimeline }>(
    `/api/v1/admin/ordering/orders/${encodeURIComponent(agencyOrderId)}/timeline`,
  );
}

export async function getOperatorOrderInvestigation(agencyOrderId: string) {
  return request<{ schemaVersion: string; investigation: OrderInvestigation }>(
    `/api/v1/admin/ordering/orders/${encodeURIComponent(agencyOrderId)}`,
  );
}

export type OrderAccountingSummary = {
  providerEnvironment: AccountingEnvironment;
  currency: "USD";
  actualCustomerGrossInMinor: number;
  actualProcessorFeeMinor: number;
  actualNetCashInMinor: number;
  actualMerchantSpendMinor: number;
  actualCustomerCompensatedMinor: number;
  actualMerchantRecoveredMinor: number;
  unreconciledCashGrossMinor: number;
  realizedBalanceMinor: number;
  forecastNetCashInMinor: number;
  forecastProcessorFeeMinor: number;
  forecastMerchantSpendMinor: number;
  expectedCompensationMinor: number;
  forecastAdjustmentMinor: number;
  forecastBalanceMinor: number;
  orderCount: number;
  attentionOrderCount: number;
  orders: OrderAccountingProjection[];
  asOf: string;
};

export async function retryProcessEffect(effectId: string) {
 return applyOwnerAction(`/api/v1/admin/ordering/process-effects/${encodeURIComponent(effectId)}/retry`, { method: "POST", body: "{}" });
}

export async function listOperatorWorkItems(view: "OPEN" | "RESOLVED" = "OPEN") {
  return request<{ schemaVersion: string; items: OperatorWorkItem[]; counts: OperatorWorkItemCounts }>(
    `/api/v1/admin/ordering/work-items?view=${view}`,
  );
}

// kind별 전역 카운트(SQL COUNT — 목록 limit 캡 무관, ADR-0057 2차 P2).
// nav의 예외 처리 뱃지가 이 값만 신뢰한다.
export async function listOperatorWorkItemCounts() {
	return request<{ schemaVersion: string; counts: OperatorWorkItemCounts; livePayPalOrderCount?: number }>(
		"/api/v1/admin/ordering/work-items/counts",
	);
}

export async function adoptPayPalReauthorization(
  item: Pick<PayPalResourceAdoptionItem, "operationId" | "merchantOrderId">,
  input: PayPalReauthorizationAdoptionInput,
) {
  return request<{
    schemaVersion: "vitlane.paypal-reauthorization-adoption.v1";
    result: { adoption: Record<string, unknown>; replay: boolean };
  }>(
    `/api/v1/admin/payment/paypal/merchant-orders/${encodeURIComponent(item.merchantOrderId)}/reauthorization-adoptions`,
    {
      method: "POST",
      headers: {
        "Idempotency-Key": `paypal-reauthorization-adoption:${item.operationId}:${input.evidenceHash.replace(/^0x/, "")}`,
      },
      body: JSON.stringify(input),
    },
  );
}

export async function adoptPayPalMORefund(
  compensationId: string,
  input: PayPalMORefundAdoptionInput,
) {
  return request<{
    schemaVersion: "vitlane.paypal-mo-refund-adoption.v1";
    result: { compensation: Record<string, unknown>; adoption: Record<string, unknown>; replay: boolean };
  }>(
    `/api/v1/admin/payment/paypal/mo-compensations/${encodeURIComponent(compensationId)}/refund-adoptions`,
    { method: "POST", body: JSON.stringify(input) },
  );
}

export async function getOrderAccounting(environment: AccountingEnvironment) {
  return request<{ schemaVersion: "vitlane.order-accounting.v2"; summary: OrderAccountingSummary }>(
    `/api/v1/admin/ordering/order-accounting?environment=${environment}`,
  );
}

// claim 키는 시도당 새 UUID다 — 고정 키는 서버가 replay로 흡수해 같은
// 운영자의 lease 연장(재담당)이 영영 불가능해진다(운영정합 5차 B1).
export async function claimProcurementTask(taskId: string) {
  return applyOwnerAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/claim`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `${operationKey(taskId, "claim")}:${crypto.randomUUID()}` },
      body: "{}",
    },
  );
}

// 열람 키도 시도당 새 UUID다(운영정합 5차 B4) — 고정 키는 최초 승인의
// replay만 돌려줘 이후의 접근·새 사유가 PII 감사에 남지 않았고, DENIED
// 감사가 커밋되는 B3 수선 뒤에는 고정 키를 영구 오염시킨다(replay 비교가
// GRANTED를 요구). 이제 열람마다 감사 hash chain에 새 행이 남는다.
export async function revealProcurementShipping(taskId: string, reasonDetail: string) {
  const receipt = await applyOwnerAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/shipping-address-reveals`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `${operationKey(taskId, "shipping")}:${crypto.randomUUID()}` },
      body: JSON.stringify({
        reasonCode: "PLACE_MERCHANT_ORDER",
        reasonDetail,
        correlationId: `procurement-operator:${taskId}`,
      }),
    },
  );
  return request<{ shippingAddress: OperatorShippingAddress }>(`/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/process-requests/${encodeURIComponent(receipt.requestId)}/reveal`);
}

// 운용 참조 정보 인계(ADR-0052 §4.4): 만료면 410. 실제 구매는 이 URL을
// 재사용/refresh하거나 자동 fallback하지 않고 주문 sheet를 보고 수동 수행한다.
export async function revealProcurementContinueURL(taskId: string, reasonDetail: string) {
  const receipt = await applyOwnerAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/continue-url-reveals`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `${operationKey(taskId, "continue-url")}:${crypto.randomUUID()}` },
      body: JSON.stringify({
        reasonCode: "PLACE_MERCHANT_ORDER",
        reasonDetail,
        correlationId: `procurement-operator:${taskId}`,
      }),
    },
  );
  return request<{ continueUrl: string; continueUrlHash: string }>(`/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/process-requests/${encodeURIComponent(receipt.requestId)}/reveal`);
}

export type ProcurementManualDecision = {
  id: string;
  merchantOrderId: string;
  agencyOrderId: string;
  taskId: string;
  decision: "WITHIN_AUTHORIZATION" | "IMMATERIAL_VARIANCE" | "MATERIAL_NEW_CONDITION" | "UNABLE_TO_PURCHASE";
  publicRationale: string;
  internalNote?: string;
  observedCondition: string;
  evidenceSource: "OPERATOR_OBSERVATION" | "MERCHANT_PAGE" | "MERCHANT_POLICY" | "RECEIPT" | "OTHER";
  evidenceHash: string;
  observedAt: string;
  authorizationHash: string;
  executionProfileHash: string;
  createdAt: string;
};

export type ProcurementCustomerRequest = {
  id: string;
  merchantOrderId: string;
  agencyOrderId: string;
  sourceDecisionId: string;
  kind: "INFORMATION" | "CONSENT";
  prompt: string;
  responseType: "TEXT" | "SINGLE_CHOICE" | "BOOLEAN_CONSENT";
  responseOptions?: string[];
  publicContext: string;
  state: "PENDING" | "ANSWERED" | "DECLINED" | "FAILED_NO_RESPONSE" | "CANCELLED";
  response?: unknown;
  requestedAt: string;
  dueAt: string;
  resolvedAt?: string;
  resolutionReason?: string;
  version: number;
};

export async function getProcurementManualReview(taskId: string) {
  return request<{
    schemaVersion: string;
    decisions: ProcurementManualDecision[];
    customerRequests: ProcurementCustomerRequest[];
  }>(`/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/manual-review`);
}

export async function recordProcurementManualDecision(
  taskId: string,
  input: {
    decision: ProcurementManualDecision["decision"];
    publicRationale: string;
    internalNote?: string;
    observedCondition: string;
    evidenceSource: ProcurementManualDecision["evidenceSource"];
  },
) {
	const hash = await procurementEvidenceHash(input.evidenceSource, input.observedCondition);
  return applyOwnerAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/decisions`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `${operationKey(taskId, "decision")}:${crypto.randomUUID()}` },
      body: JSON.stringify({ ...input, evidenceHash: hash, observedAt: new Date().toISOString() }),
    },
  );
}

export async function createProcurementCustomerRequest(
  taskId: string,
	input: {
		observedCondition: string;
		internalNote?: string;
		evidenceSource: ProcurementManualDecision["evidenceSource"];
		kind: ProcurementCustomerRequest["kind"];
    prompt: string;
    responseType: ProcurementCustomerRequest["responseType"];
    responseOptions?: string[];
    publicContext: string;
  },
) {
	const evidenceHash = await procurementEvidenceHash(input.evidenceSource, input.observedCondition);
	return applyOwnerAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/customer-requests`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `${operationKey(taskId, "customer-request")}:${crypto.randomUUID()}` },
			body: JSON.stringify({
				...input,
				evidenceHash,
				observedAt: new Date().toISOString(),
			}),
		},
	);
}

async function procurementEvidenceHash(
	evidenceSource: ProcurementManualDecision["evidenceSource"],
	observedCondition: string,
) {
	const evidenceHash = await crypto.subtle.digest(
		"SHA-256",
		new TextEncoder().encode(`${evidenceSource}:${observedCondition}`),
	);
	return Array.from(
		new Uint8Array(evidenceHash),
		(byte) => byte.toString(16).padStart(2, "0"),
	).join("");
}

export async function resolveProcurementCustomerRequest(
  requestId: string,
  expectedVersion: number,
  cancel: boolean,
  reason: string,
) {
  return applyOwnerAction(
    `/api/v1/admin/procurement/customer-requests/${encodeURIComponent(requestId)}/resolution`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `${operationKey(requestId, "request-resolution")}:${crypto.randomUUID()}` },
      body: JSON.stringify({ expectedVersion, cancel, reason }),
    },
  );
}

export async function beginProcurementMerchantEffect(taskId: string) {
  return submitProcessAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/merchant-effect`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `${operationKey(taskId, "merchant-effect")}:${crypto.randomUUID()}` },
      body: "{}",
    },
  );
}

// done 결과의 단일 창구(ADR-0053 — 모드 무관 PLACEMENT_PENDING→PLACED).
// Sandbox와 Live 모두 evidence가 필수이고 kind가 경제적 의미를 분리한다.
export async function completeProcurementTask(
  taskId: string,
  evidence: ProcurementPlacementEvidenceInput,
) {
  return applyOwnerAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/result`,
    {
      method: "POST",
      headers: { "Idempotency-Key": operationKey(taskId, "done") },
      body: JSON.stringify({
        done: true,
		...evidence,
      }),
    },
  );
}

export async function failProcurementTask(taskId: string, failureCode: string) {
  return applyOwnerAction(
    `/api/v1/admin/procurement/tasks/${encodeURIComponent(taskId)}/result`,
    {
      method: "POST",
      headers: { "Idempotency-Key": operationKey(taskId, `fail:${failureCode}`) },
      body: JSON.stringify({ done: false, failureCode }),
    },
  );
}

// 종전 publishOrderNotice(고지함 발행)는 ADR-0059로 Support 대화에 흡수됐다 —
// 주문 안내 발신은 products/support/infra/supportOperatorApi.ts의
// sendSupportOrderMessage가 소유한다.

function operationKey(subject: string, action: string) {
  return `procurement:${subject}:${action}`;
}

// --- 환불 요청 심사 큐 (Phase 8 Step 3 PR-2, ADR-0050) ---

export type RefundReviewMoney = {
  amountMinor: number;
  currency: string;
};

export type RefundReviewDeliveryFacts = {
  recorded: boolean;
  expectedFulfillment?: string;
  shipmentState?: string;
  carrier?: string;
  trackingRef?: string;
  latestEventStatus?: string;
  latestEventNote?: string;
  latestEventOccurredAt?: string;
  resolutionCause?: string;
  resolutionDecision?: string;
  resolutionNote?: string;
  resolutionRecordedAt?: string;
};

export type RefundReviewReturnFacts = {
  recorded: boolean;
  state?: string;
  merchantDisposition?: string;
  note?: string;
  updatedAt?: string;
};

export type RefundReviewLine = {
  lineId: string;
  quantity: number;
  productUrl?: string;
  productTitle: string;
  variantId?: string;
  variantTitle?: string;
  selectedOptions: string[];
};

export type RefundReviewUnitFact = {
	merchantOrderUnitId: string;
	lineId: string;
	unitIndex: number;
	disposition: string;
  deliveryFacts: RefundReviewDeliveryFacts;
  returnFacts: RefundReviewReturnFacts;
};

export type RefundReviewContext = {
  orderNumber: string;
	merchantOrderId: string;
	allocationId: string;
	shopDomain: string;
	merchantId: string;
	externalOrderRef?: string;
	merchantOrderState: string;
	requestedGrossAmount: RefundReviewMoney;
	lines: RefundReviewLine[];
	units: RefundReviewUnitFact[];
	internalNote?: string;
};

export type OperatorRefundRequest = {
  id: string;
  agencyOrderId: string;
	merchantOrderId: string;
	allocationId: string;
	requestedGrossAmount: RefundReviewMoney;
  state: "REQUESTED" | "REVIEWING" | "RESOLVED";
  reasonCode: string;
  publicRationale: string;
	decision?: "APPROVED" | "REJECTED";
	decisionPublicRationale?: string;
	decidedAt?: string;
  reviewContext?: RefundReviewContext;
  createdAt: string;
  updatedAt: string;
};

export async function listRefundQueue() {
  return request<{ schemaVersion: string; refundRequests: OperatorRefundRequest[] }>(
    "/api/v1/admin/agencyOrder/refund-requests",
  );
}

export async function decideRefundRequest(
  requestId: string,
	decision: {
    approve: boolean;
    publicRationale: string;
    internalNote?: string;
	},
) {
  return applyOwnerAction(
    `/api/v1/admin/agencyOrder/refund-requests/${encodeURIComponent(requestId)}/decisions`,
		{ method: "POST", body: JSON.stringify(decision) },
  );
}

// --- Logistics 운영자 창구(계약 v7 §9, ADR-0053 — 양 모드 동일 워크플로) ---

export type OperatorShipment = {
  id: string;
  agencyOrderId: string;
  merchantOrderId: string;
  carrier: string;
  trackingRef: string;
  state: "CREATED" | "LABEL_CREATED" | "IN_TRANSIT" | "OUT_FOR_DELIVERY" | "DELIVERED" |
    "EXCEPTION" | "LOST" | "RETURN_TO_SENDER" | "RETURNED" | "CANCELLED_NO_EFFECT" |
    "EXCEPTION_RECONCILIATION";
  version: number;
  updatedAt: string;
};

export type OperatorExpectedUnit = {
  id: string;
  merchantOrderUnitId: string;
  merchantOrderId: string;
  agencyOrderId: string;
  lineId: string;
  unitIndex: number;
  fulfillment: string;
  resolution?: {
    id: string;
    cause: "MISSING" | "WRONG_ACTUAL" | "LOST";
    decision: "REFUND" | "DELIVERED_OK";
    note?: string;
    createdAt: string;
  };
  returnState?: string;
  compensationAction?: string;
  compensationState?: string;
  updatedAt: string;
};

export type OperatorShipmentView = {
  shipment: OperatorShipment;
  units: OperatorExpectedUnit[];
  events: Array<{ id: string; status: string; note?: string; occurredAt: string }>;
  actions: Array<"RECORD_IN_TRANSIT" | "CONFIRM_DELIVERY_OUTCOME">;
};

export async function listOrderShipments(agencyOrderId: string) {
  return request<{ schemaVersion: string; shipments: OperatorShipmentView[] }>(
    `/api/v1/admin/logistics/agencyOrder/${encodeURIComponent(agencyOrderId)}/shipments`,
  );
}

export async function createShipment(
  merchantOrderId: string,
  carrier: string,
  trackingRef: string,
  expectedUnitIds: string[] = [],
) {
  return applyOwnerAction(
    "/api/v1/admin/logistics/shipments",
    { method: "POST", body: JSON.stringify({ merchantOrderId, carrier, trackingRef, expectedUnitIds }) },
  );
}

export async function recordShipmentEvent(shipmentId: string, status: string, note = "") {
  return applyOwnerAction(
    `/api/v1/admin/logistics/shipments/${encodeURIComponent(shipmentId)}/events`,
    { method: "POST", body: JSON.stringify({ status, note, occurredAt: new Date().toISOString() }) },
  );
}

// 수령 일괄 확인 — exceptions는 expectedUnitId → "MISSING" | "WRONG_ACTUAL".
export async function confirmShipmentDelivered(
  shipmentId: string,
  exceptions: Record<string, "MISSING" | "WRONG_ACTUAL"> = {},
) {
  return applyOwnerAction(
    `/api/v1/admin/logistics/shipments/${encodeURIComponent(shipmentId)}/delivered-confirmation`,
    { method: "POST", body: JSON.stringify({ exceptions }) },
  );
}

export async function listLogisticsExceptions() {
  return request<{ schemaVersion: string; units: OperatorExpectedUnit[] }>(
    "/api/v1/admin/logistics/exceptions",
  );
}

export async function resolveLogisticsException(
  expectedUnitId: string,
  decision: "REFUND" | "DELIVERED_OK",
  note = "",
) {
  return applyOwnerAction(
    `/api/v1/admin/logistics/units/${encodeURIComponent(expectedUnitId)}/resolution`,
    { method: "POST", body: JSON.stringify({ decision, note }) },
  );
}

export type OperatorReturn = {
  id: string;
  expectedUnitId: string;
  agencyOrderId: string;
  state: "REQUESTED" | "RETURN_IN_TRANSIT" | "RECEIVED" | "MERCHANT_RETURNED" | "CLOSED" | "CANCELLED";
  merchantDisposition?: string;
};

export async function listLogisticsReturns() {
  return request<{ schemaVersion: string; returns: OperatorReturn[] }>(
    "/api/v1/admin/logistics/returns",
  );
}

export async function createLogisticsReturn(expectedUnitId: string, note = "") {
  return applyOwnerAction(
    "/api/v1/admin/logistics/returns",
    { method: "POST", body: JSON.stringify({ expectedUnitId, note }) },
  );
}

export async function updateLogisticsReturn(
  returnId: string,
  state: "" | "RETURN_IN_TRANSIT" | "RECEIVED" | "MERCHANT_RETURNED" | "CLOSED" | "CANCELLED",
  merchantDisposition = "",
  note = "",
) {
  return applyOwnerAction(
    `/api/v1/admin/logistics/returns/${encodeURIComponent(returnId)}`,
    { method: "POST", body: JSON.stringify({ state, merchantDisposition, note }) },
  );
}

// --- 간이 회수 원장 기입 (운영정합 5차 PR-D, ADR-0052 §5.2) ---
// 상태는 서버가 금액에서 파생한다. 자동 entry(Return 처분·지연 취소)와 수동
// entry가 한 목록으로 오고, 수동 행만 삭제할 수 있다(자동은 waive로).

export type RecoveryEntry = {
  id: string;
  merchantOrderId: string;
  agencyOrderId: string;
  cause: "CHARGE_WITHOUT_ORDER" | "MERCHANT_CANCEL" | "RETURN" | "COST_ADJUSTMENT" | "OTHER";
  expectedAmountMinor: number;
  receivedAmountMinor: number;
  state: "EXPECTED" | "RECEIVED" | "WAIVED" | "LOSS" | "OVER_RECOVERED";
  manual: boolean;
  note?: string;
  version: number;
  createdAt: string;
  updatedAt: string;
};

export type RecoverySurface = {
  agencyOrderId: string;
  matchedBy: "AGENCY_ORDER" | "MERCHANT_ORDER";
  focusMerchantOrderId?: string;
  entries: RecoveryEntry[];
  merchantOrders: Array<{ id: string; shopDomain: string; checkoutOrdinal: number }>;
};

export async function listRecoveryEntries(referenceId: string) {
  return request<{ schemaVersion: string; surface: RecoverySurface }>(
    `/api/v1/admin/procurement/recovery-entries?referenceId=${encodeURIComponent(referenceId)}`,
  );
}

export async function createRecoveryEntry(input: {
  merchantOrderId: string;
  cause: RecoveryEntry["cause"];
  expectedAmountMinor: number;
  receivedAmountMinor: number;
  note: string;
}) {
  return request<{ entry: RecoveryEntry; replay: boolean }>(
    "/api/v1/admin/procurement/recovery-entries",
    {
      method: "POST",
      headers: { "Idempotency-Key": `recovery:create:${crypto.randomUUID()}` },
      body: JSON.stringify(input),
    },
  );
}

export async function recordRecoveryEntry(
  entryId: string,
  receivedAmountMinor: number,
  note: string,
  expectedVersion: number,
) {
  return request<{ entry: RecoveryEntry; replay: boolean }>(
    `/api/v1/admin/procurement/recovery-entries/${encodeURIComponent(entryId)}/record`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `recovery:record:${crypto.randomUUID()}` },
      body: JSON.stringify({ receivedAmountMinor, note, expectedVersion }),
    },
  );
}

export async function waiveRecoveryEntry(entryId: string, note: string, expectedVersion: number) {
  return request<{ entry: RecoveryEntry; replay: boolean }>(
    `/api/v1/admin/procurement/recovery-entries/${encodeURIComponent(entryId)}/waive`,
    {
      method: "POST",
      headers: { "Idempotency-Key": `recovery:waive:${crypto.randomUUID()}` },
      body: JSON.stringify({ note, expectedVersion }),
    },
  );
}

export async function deleteRecoveryEntry(entryId: string, expectedVersion: number) {
  return request<{ deleted: true }>(
    `/api/v1/admin/procurement/recovery-entries/${encodeURIComponent(entryId)}`,
    { method: "DELETE", body: JSON.stringify({ expectedVersion }) },
  );
}
