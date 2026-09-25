import { useState } from "react";
import { Link } from "react-router";
import {
  Button, FeedbackState, Field, Input, NativeSelect, NativeSelectOption,
  Notice, PageHeader, Textarea,
} from "../../../shared/ui";
import { textConstraint } from "../../../shared/forms/textConstraint";
import { invariantContent, useLocale } from "../../../shared/i18n";
import { APIError } from "../../../shared/api/client";
import {
  adoptPayPalMORefund,
  adoptPayPalReauthorization,
  type PayPalResourceAdoptionItem,
  type PaymentReconciliationItem,
} from "../infra/agencyOrderOperatorApi";

type Draft = {
  providerResourceId: string;
  evidenceSource: "PAYPAL_DASHBOARD" | "PAYPAL_SUPPORT" | "PAYPAL_API" | "PAYPAL_WEBHOOK" | "OTHER";
  evidenceHash: string;
  note: string;
  observedAt: string;
};

const evidenceHashPattern = /^0x[0-9a-f]{64}$/;
const providerResourcePattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$/;

export function PayPalResourceAdoptionPanel({
  reconciliations,
  adoptions,
  can,
  run,
  working,
}: {
  reconciliations: PaymentReconciliationItem[];
  adoptions: PayPalResourceAdoptionItem[];
  can: (kind: string, id: string, action: string) => boolean;
  run: (key: string, action: () => Promise<void>) => Promise<void>;
  working?: string;
}) {
  const { l, locale } = useLocale();
  return <section aria-label={l("Payment reconciliation", "결제 대사")} className="catalog-ui-refund-queue paypal-resource-adoption">
    <PageHeader
      eyebrow={l("Payment Reconciliation", "결제 대사")}
      title={l("Payment reconciliation ({count})", "결제 대사 ({count})", { count: reconciliations.length + adoptions.length })}
      description={l(
        "Review uncertain payment outcomes. Manual PayPal resource adoption is available only after the original provider request window expired and no resource ID was stored.",
        "미확정 결제 결과를 확인합니다. PayPal resource 수동 채택은 원래 provider 요청 기한이 끝났고 resource ID가 저장되지 않은 경우에만 열립니다.",
      )}
    />
    <Notice tone="warning">
      <strong>{l("Existing-resource verification only.", "기존 resource 검증 전용입니다.")}</strong>{" "}
      {l(
        "This action never creates a new PayPal authorization or refund. The server performs a fresh GET for the ID you enter and adopts it only when every stored amount and binding matches exactly.",
        "이 행동은 새 PayPal 승인이나 환불을 만들지 않습니다. 서버는 입력한 ID를 fresh GET하고 저장된 금액·연결이 모두 정확히 일치할 때만 채택합니다.",
      )}
    </Notice>
    {reconciliations.map((item) => <article className="paypal-resource-adoption__read-only" key={item.paymentId}>
      <div><strong>{l("Customer payment needs reconciliation", "고객 결제 대사 필요")}</strong><span>{item.providerEnvironment} · {item.paymentState}</span></div>
      <p>{l("Payment {payment} · PayPal order {paypalOrder} · reason {reason}", "결제 {payment} · PayPal 주문 {paypalOrder} · 원인 {reason}", {
        payment: item.paymentId,
        paypalOrder: item.paypalOrderId || "—",
        reason: item.reasonCode || "—",
      })}</p>
      <p className="vt-field__hint">{l("No manual resource-adoption action is eligible for this payment state.", "이 결제 상태에는 수동 resource 채택 행동이 열리지 않습니다.")}</p>
    </article>)}
    {adoptions.map((item) => <PayPalResourceAdoptionCard
      can={can}
      item={item}
      key={item.operationId}
      locale={locale}
      run={run}
      working={working}
    />)}
    {reconciliations.length === 0 && adoptions.length === 0 ? <FeedbackState
      description={l("Uncertain payment operations appear here when operator evidence can safely reconcile them.", "운영자 증거로 안전하게 대사할 수 있는 미확정 결제 작업이 생기면 여기에 표시됩니다.")}
      state="empty"
      title={l("No payment reconciliation is waiting", "대기 중인 결제 대사가 없습니다")}
    /> : null}
  </section>;
}

function PayPalResourceAdoptionCard({ item, can, run, working, locale }: {
  item: PayPalResourceAdoptionItem;
  can: (kind: string, id: string, action: string) => boolean;
  run: (key: string, action: () => Promise<void>) => Promise<void>;
  working?: string;
  locale: string;
}) {
  const { l } = useLocale();
  const isReauthorization = item.reconciliationKind === "PAYPAL_REAUTHORIZATION";
  const action = isReauthorization ? "ADOPT_PAYPAL_REAUTHORIZATION" : "ADOPT_PAYPAL_MO_REFUND";
  const [draft, setDraft] = useState<Draft>(() => ({
    providerResourceId: "",
    evidenceSource: "PAYPAL_DASHBOARD",
    evidenceHash: "",
    note: "",
    observedAt: localDateTime(new Date()),
  }));
  const normalizedHash = draft.evidenceHash.trim().toLowerCase();
  const noteConstraint = isReauthorization
    ? textConstraint({ value: draft.note, max: 4_000, required: false, l })
    : textConstraint({ value: draft.note, min: 1, max: 2_000, l });
  const observed = new Date(draft.observedAt);
  const noteValid = noteConstraint.ready;
  const valid = providerResourcePattern.test(draft.providerResourceId.trim()) &&
    evidenceHashPattern.test(normalizedHash) && noteValid && !Number.isNaN(observed.getTime()) &&
    can("PAYMENT_RECONCILIATION", item.operationId, action);
  const busyKey = `${item.operationId}:adopt`;

  const submit = async () => {
    try {
      const observedAt = observed.toISOString();
      if (isReauthorization) {
        await adoptPayPalReauthorization(item, {
          providerAuthorizationId: draft.providerResourceId.trim(),
          evidenceSource: draft.evidenceSource === "PAYPAL_SUPPORT" || draft.evidenceSource === "OTHER"
            ? draft.evidenceSource
            : "PAYPAL_DASHBOARD",
          evidenceHash: normalizedHash,
          internalNote: draft.note.trim() || undefined,
          observedAt,
        });
        return;
      }
      if (!item.compensationId) throw new Error(l("The compensation reference is missing.", "보상 reference가 없습니다."));
      await adoptPayPalMORefund(item.compensationId, {
        providerRefundId: draft.providerResourceId.trim(),
        publicRationale: draft.note.trim(),
        evidenceSource: draft.evidenceSource === "PAYPAL_SUPPORT" ? "OTHER" : draft.evidenceSource,
        evidenceHash: normalizedHash,
        observedAt,
      });
    } catch (caught) {
      throw new Error(payPalResourceAdoptionError(caught, l));
    }
  };

  return <article className="paypal-resource-adoption__card">
    <header>
      <div>
        <strong>{isReauthorization ? l("Adopt existing PayPal authorization", "기존 PayPal 승인 채택") : l("Adopt existing PayPal refund", "기존 PayPal 환불 채택")}</strong>
        <span>{item.providerEnvironment} · {item.operationState} · {l("deadline", "기한")} {new Date(item.idempotencyDeadline).toLocaleString(locale)}</span>
      </div>
      <span className={item.providerEnvironment === "LIVE" ? "paypal-dispute-card__live" : "paypal-dispute-card__sandbox"}>{item.providerEnvironment}</span>
    </header>
    <dl>
      <div><dt>{l("Order", "주문")}</dt><dd><Link to={`/admin/agencyOrder/${encodeURIComponent(item.agencyOrderId)}`}>{item.agencyOrderId}</Link></dd></div>
      <div><dt>{l("MerchantOrder", "MerchantOrder")}</dt><dd><code>{item.merchantOrderId}</code></dd></div>
      {item.compensationId ? <div><dt>{l("Compensation", "보상")}</dt><dd><code>{item.compensationId}</code></dd></div> : null}
      <div><dt>{l("Exact amount", "정확한 금액")}</dt><dd>{formatMinor(item.amountMinor, item.currency, locale)}</dd></div>
      <div><dt>{l("Stored owner state", "저장된 owner 상태")}</dt><dd>{item.ownerState}</dd></div>
      <div><dt>{l("Last reason", "최근 원인")}</dt><dd>{item.reasonCode || "—"}</dd></div>
    </dl>
    <div className="paypal-resource-adoption__form">
      <Field hint={l("Enter the ID of the resource already visible in PayPal. This does not trigger a provider write.", "PayPal에 이미 보이는 resource ID를 입력하세요. provider write는 실행되지 않습니다.")} id={`paypal-resource-${item.operationId}`} label={isReauthorization ? l("Existing PayPal authorization ID", "기존 PayPal 승인 ID") : l("Existing PayPal refund ID", "기존 PayPal 환불 ID")} required>
        <Input id={`paypal-resource-${item.operationId}`} maxLength={255} onChange={(event) => setDraft((current) => ({ ...current, providerResourceId: event.target.value }))} required value={draft.providerResourceId} />
      </Field>
      <Field id={`paypal-adoption-source-${item.operationId}`} label={l("Evidence source", "증거 출처")} required>
        <NativeSelect id={`paypal-adoption-source-${item.operationId}`} onChange={(event) => setDraft((current) => ({ ...current, evidenceSource: event.target.value as Draft["evidenceSource"] }))} value={draft.evidenceSource}>
          <NativeSelectOption value="PAYPAL_DASHBOARD">{l("PayPal dashboard", "PayPal 대시보드")}</NativeSelectOption>
          {isReauthorization ? <NativeSelectOption value="PAYPAL_SUPPORT">{l("PayPal support", "PayPal 지원")}</NativeSelectOption> : null}
          {!isReauthorization ? <NativeSelectOption value="PAYPAL_API">{l("PayPal API observation", "PayPal API 관찰")}</NativeSelectOption> : null}
          {!isReauthorization ? <NativeSelectOption value="PAYPAL_WEBHOOK">{l("PayPal webhook evidence", "PayPal webhook 증거")}</NativeSelectOption> : null}
          <NativeSelectOption value="OTHER">{l("Other", "기타")}</NativeSelectOption>
        </NativeSelect>
      </Field>
      <Field error={draft.evidenceHash && !evidenceHashPattern.test(normalizedHash) ? l("Use a 0x-prefixed lowercase SHA-256 hash.", "0x로 시작하는 소문자 SHA-256 hash를 입력하세요.") : undefined} id={`paypal-adoption-hash-${item.operationId}`} label={l("Evidence SHA-256", "증거 SHA-256")} required>
        <Input id={`paypal-adoption-hash-${item.operationId}`} maxLength={66} onChange={(event) => setDraft((current) => ({ ...current, evidenceHash: event.target.value }))} placeholder={invariantContent("0x…")} required value={draft.evidenceHash} />
      </Field>
      <Field id={`paypal-adoption-observed-${item.operationId}`} label={l("Observed at", "관찰 시각")} required>
        <Input id={`paypal-adoption-observed-${item.operationId}`} onChange={(event) => setDraft((current) => ({ ...current, observedAt: event.target.value }))} required type="datetime-local" value={draft.observedAt} />
      </Field>
      <Field error={noteConstraint.error} hint={`${isReauthorization ? l("Optional internal operations/accounting note. Never shown to the customer.", "선택 운영·회계 내부 메모이며 고객에게 표시되지 않습니다.") : l("Required customer-visible rationale for why this existing refund is being adopted.", "이 기존 환불을 채택하는 이유이며 고객에게 공개되는 필수 근거입니다.")} ${noteConstraint.hint}`} id={`paypal-adoption-note-${item.operationId}`} label={isReauthorization ? l("Internal reconciliation note", "내부 대사 메모") : l("Customer-visible rationale", "고객 공개 근거")} required={!isReauthorization}>
        <Textarea id={`paypal-adoption-note-${item.operationId}`} maxLength={isReauthorization ? 4_000 : 2_000} onChange={(event) => setDraft((current) => ({ ...current, note: event.target.value }))} required={!isReauthorization} value={draft.note} />
      </Field>
      {can("PAYMENT_RECONCILIATION", item.operationId, action) ? <Button busy={working === busyKey} disabled={!valid || Boolean(working)} emphasis="primary" onClick={() => void run(busyKey, submit)} type="button">
        {isReauthorization ? l("Verify and adopt existing authorization", "기존 승인 검증 후 채택") : l("Verify and adopt existing refund", "기존 환불 검증 후 채택")}
      </Button> : null}
    </div>
  </article>;
}

function localDateTime(value: Date) {
  const offset = value.getTimezoneOffset() * 60_000;
  return new Date(value.getTime() - offset).toISOString().slice(0, 16);
}

function formatMinor(amountMinor: number, currency: string, locale: string) {
  return new Intl.NumberFormat(locale, { style: "currency", currency }).format(amountMinor / 100);
}

function payPalResourceAdoptionError(caught: unknown, l: ReturnType<typeof useLocale>["l"]) {
  if (!(caught instanceof APIError)) {
    return caught instanceof Error
      ? caught.message
      : l("We couldn't verify the PayPal resource.", "PayPal resource를 검증하지 못했습니다.");
  }
  switch (caught.code) {
    case "PAYPAL_RESOURCE_ADOPTION_INVALID":
      return l("Check the resource ID, evidence, note, and observation time.", "resource ID, 증거, 메모와 관찰 시각을 확인하세요.");
    case "PAYPAL_RESOURCE_ADOPTION_NOT_AVAILABLE":
      return l("This operation is no longer eligible. Refresh the payment reconciliation list.", "이 작업은 더 이상 채택 대상이 아닙니다. 결제 대사 목록을 새로고침하세요.");
    case "PAYPAL_RESOURCE_ADOPTION_MISMATCH":
      return l("PayPal's resource does not exactly match the stored amount or binding. Nothing was adopted.", "PayPal resource가 저장된 금액 또는 연결과 정확히 일치하지 않습니다. 아무것도 채택하지 않았습니다.");
    case "PAYPAL_RESOURCE_ADOPTION_CONFLICT":
      return l("Payment state changed while verifying. Refresh and review the latest state.", "검증 중 결제 상태가 바뀌었습니다. 새로고침 후 최신 상태를 확인하세요.");
    case "PAYPAL_RESOURCE_ADOPTION_UNAVAILABLE":
      return l("PayPal could not be queried for exact verification. Try again after checking provider availability.", "정확 검증을 위해 PayPal을 조회하지 못했습니다. provider 상태를 확인한 뒤 다시 시도하세요.");
    default:
      return l("We couldn't verify the PayPal resource. ({code})", "PayPal resource를 검증하지 못했습니다. ({code})", { code: caught.code });
  }
}
