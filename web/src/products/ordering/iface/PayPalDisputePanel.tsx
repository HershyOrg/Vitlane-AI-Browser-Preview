import { useEffect, useState } from "react";
import { Link } from "react-router";
import {
  Button, FeedbackState, Field, Input, NativeSelect, NativeSelectOption,
  Notice, PageHeader, Textarea,
} from "../../../shared/ui";
import { textConstraint } from "../../../shared/forms/textConstraint";
import { invariantContent, useLocale } from "../../../shared/i18n";
import {
  listPayPalDisputes,
  recordPayPalDisputeAction,
  type PayPalDisputeActionInput,
  type PayPalDisputeCase,
  type PayPalDisputeProviderStatus,
  type PayPalDisputeState,
  type PayPalEnvironment,
} from "../infra/paypalDisputeApi";

type Draft = Omit<PayPalDisputeActionInput, "expectedVersion" | "observedAt"> & {
  observedAt: string;
};

const evidenceHashPattern = /^0x[0-9a-f]{64}$/;

export function PayPalDisputePanel() {
  const { l, locale } = useLocale();
  const [environment, setEnvironment] = useState<PayPalEnvironment>("SANDBOX");
  const [state, setState] = useState<PayPalDisputeState | "ALL">("OPEN");
  const [cases, setCases] = useState<PayPalDisputeCase[]>();
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string>();
  const [refresh, setRefresh] = useState(0);

  useEffect(() => {
    let active = true;
    setCases(undefined);
    setError(undefined);
    void listPayPalDisputes(environment, state)
      .then((result) => { if (active) setCases(result.disputes); })
      .catch(() => {
        if (active) {
          setCases([]);
          setError(l(
            "We couldn't load the PayPal dispute queue.",
            "PayPal 분쟁 큐를 불러오지 못했습니다.",
          ));
        }
      });
    return () => { active = false; };
  }, [environment, l, refresh, state]);

  const draftOf = (item: PayPalDisputeCase): Draft => drafts[item.id] ?? initialDraft(item);
  const updateDraft = (item: PayPalDisputeCase, patch: Partial<Draft>) =>
    setDrafts((current) => ({
      ...current,
      [item.id]: { ...(current[item.id] ?? initialDraft(item)), ...patch },
    }));

  async function submit(item: PayPalDisputeCase) {
    const draft = draftOf(item);
    setBusy(item.id);
    setError(undefined);
    try {
      // Bind the write to the case itself. A filter change can briefly leave
      // the previous render on screen until the next effect runs; using the
      // selected filter here would otherwise turn that UI race into a
      // cross-environment request (which the server correctly rejects).
      await recordPayPalDisputeAction(item.environment, item.id, {
        ...draft,
        expectedVersion: item.version,
        observedAt: new Date(draft.observedAt).toISOString(),
        observedOutcome: draft.observedProviderStatus === "RESOLVED"
          ? draft.observedOutcome || "NONE"
          : "NONE",
      });
      setDrafts((current) => {
        const next = { ...current };
        delete next[item.id];
        return next;
      });
      setRefresh((value) => value + 1);
    } catch {
      setError(l(
        "The action wasn't recorded. Refresh the case and verify its version and evidence fields.",
        "행동을 기록하지 못했습니다. case를 새로고침하고 version과 증거 필드를 확인하세요.",
      ));
    } finally {
      setBusy(undefined);
    }
  }

  return <section aria-label={l("PayPal dispute operations", "PayPal 분쟁 운영")} className="catalog-ui-refund-queue paypal-dispute-panel">
    <PageHeader eyebrow="PayPal Disputes" title={l("PayPal dispute operations", "PayPal 분쟁 운영")} description={l("Handle the case in PayPal Resolution Center, then record exactly what you observed or submitted here. Vitlane does not automatically accept, contest, or settle disputes.", "PayPal Resolution Center에서 case를 직접 처리한 뒤, 관찰하거나 제출한 내용을 여기에 정확히 기록합니다. Vitlane은 분쟁을 자동 수락·항변·합의하지 않습니다.")} secondaryActions={[{ label: l("Refresh now", "지금 갱신"), onClick: () => setRefresh((value) => value + 1) }]} />
    <Notice tone={environment === "LIVE" ? "danger" : "test"}>
      <strong>{environment === "LIVE" ? invariantContent("PAYPAL · LIVE · REAL MONEY") : invariantContent("PAYPAL · SANDBOX · NO REAL VALUE")}</strong>{" "}
      {environment === "LIVE"
        ? l("This queue can represent real customer money. Verify the case in PayPal before recording an action.", "실제 고객 자금이 걸린 큐입니다. 행동 기록 전 PayPal에서 case를 확인하세요.")
        : l("This queue is isolated from Live disputes.", "이 큐는 Live 분쟁과 분리되어 있습니다.")}
    </Notice>
    <div className="paypal-dispute-panel__filters">
      <Field id="paypal-dispute-environment" label={l("Environment", "환경")}>
        <NativeSelect onChange={(event) => setEnvironment(event.target.value as PayPalEnvironment)} value={environment}>
          <NativeSelectOption value="SANDBOX">{invariantContent("PAYPAL · SANDBOX")}</NativeSelectOption>
          <NativeSelectOption value="LIVE">{invariantContent("PAYPAL · LIVE")}</NativeSelectOption>
        </NativeSelect>
      </Field>
      <Field id="paypal-dispute-state" label={l("Case state", "Case 상태")}>
        <NativeSelect onChange={(event) => setState(event.target.value as PayPalDisputeState | "ALL")} value={state}>
          <NativeSelectOption value="OPEN">{l("Open", "진행 중")}</NativeSelectOption>
          <NativeSelectOption value="RESOLVED">{l("Resolved", "종결")}</NativeSelectOption>
          <NativeSelectOption value="ALL">{l("All", "전체")}</NativeSelectOption>
        </NativeSelect>
      </Field>
    </div>
    {error ? <Notice announce tone="danger">{error}</Notice> : null}
    {cases === undefined ? <FeedbackState state="loading" description={l("Loading the environment-bound dispute queue.", "환경별 분쟁 큐를 불러오고 있습니다.")} /> : null}
    {cases?.length === 0 && !error ? <FeedbackState state="empty" title={l("No PayPal disputes in this view", "이 보기에 PayPal 분쟁이 없습니다")} description={l("Signed PayPal dispute webhooks appear here.", "서명 검증된 PayPal 분쟁 webhook이 여기에 표시됩니다.")} /> : null}
    {(cases ?? []).map((item) => {
      const draft = draftOf(item);
      const rationaleConstraint = textConstraint({ value: draft.publicRationale, min: 1, max: 2_000, l });
      const valid = item.state === "OPEN" && draft.externalReference.trim().length > 0 &&
        draft.publicRationale.trim().length > 0 && draft.publicRationale.trim().length <= 2_000 &&
        evidenceHashPattern.test(draft.evidenceHash.trim().toLowerCase()) && Boolean(draft.observedAt);
      return <article className="paypal-dispute-card" key={item.id}>
        <header>
          <div><strong>{l("PayPal case", "PayPal case")} {item.disputeId}</strong><span>{item.reason} · {item.lifecycleStage}</span>{item.state !== "OPEN" ? <small className="vt-field__hint">{l("This case is resolved. Its public outcome is already in Messages.", "종결된 case입니다. 공개 결과는 메시지에 기록되어 있습니다.")}</small> : null}</div>
          <span className={item.environment === "LIVE" ? "paypal-dispute-card__live" : "paypal-dispute-card__sandbox"}>{item.environment}</span>
        </header>
        <dl>
          <div><dt>{l("Order", "주문")}</dt><dd><Link to={`/admin/agencyOrder/${encodeURIComponent(item.agencyOrderId)}`}>{item.agencyOrderId}</Link></dd></div>
          <div><dt>{l("Provider status", "Provider 상태")}</dt><dd>{item.providerStatus}</dd></div>
          <div><dt>{l("Outcome", "결과")}</dt><dd>{item.outcome}</dd></div>
          <div><dt>{l("Observed", "관찰 시각")}</dt><dd>{new Date(item.lastObservedAt).toLocaleString(locale)}</dd></div>
          {item.sellerResponseDueAt ? <div><dt>{l("Seller response due", "판매자 응답 기한")}</dt><dd>{new Date(item.sellerResponseDueAt).toLocaleString(locale)}</dd></div> : null}
        </dl>
        {item.state === "OPEN" ? <div className="paypal-dispute-card__form">
          <Field id={`paypal-action-${item.id}`} label={l("Recorded action", "기록할 행동")} required>
            <NativeSelect onChange={(event) => updateDraft(item, { actionKind: event.target.value as Draft["actionKind"] })} value={draft.actionKind}>
              <NativeSelectOption value="CASE_OBSERVED">{l("Case observed", "Case 확인")}</NativeSelectOption>
              <NativeSelectOption value="MESSAGE_SENT">{l("Message sent", "메시지 전송")}</NativeSelectOption>
              <NativeSelectOption value="EVIDENCE_SUBMITTED">{l("Evidence submitted", "증거 제출")}</NativeSelectOption>
              <NativeSelectOption value="OFFER_MADE">{l("Offer made", "제안 제출")}</NativeSelectOption>
              <NativeSelectOption value="CLAIM_ACCEPTED">{l("Claim accepted", "Claim 수락")}</NativeSelectOption>
              <NativeSelectOption value="APPEAL_SUBMITTED">{l("Appeal submitted", "이의 제기")}</NativeSelectOption>
              <NativeSelectOption value="OTHER">{l("Other", "기타")}</NativeSelectOption>
            </NativeSelect>
          </Field>
          <Field hint={l("A safe PayPal case, message, evidence, or receipt reference — never paste credentials.", "PayPal case·메시지·증거·영수증의 안전한 reference입니다. 인증정보는 입력하지 마세요.")} id={`paypal-reference-${item.id}`} label={l("External reference", "외부 reference")} required>
            <Input maxLength={255} onChange={(event) => updateDraft(item, { externalReference: event.target.value })} required value={draft.externalReference} />
          </Field>
          <Field error={rationaleConstraint.error} hint={`${l("Sent to the customer in Messages.", "고객 메시지로 전달됩니다.")} ${rationaleConstraint.hint}`} id={`paypal-rationale-${item.id}`} label={l("Customer-visible rationale", "고객 공개 근거")} required>
            <Textarea maxLength={2_000} onChange={(event) => updateDraft(item, { publicRationale: event.target.value })} required value={draft.publicRationale} />
          </Field>
          <Field hint={l("Optional operations/accounting note. Never shown to the customer.", "선택 운영·회계 메모이며 고객에게 표시되지 않습니다.")} id={`paypal-internal-${item.id}`} label={l("Internal note", "내부 메모")}>
            <Textarea maxLength={4_000} onChange={(event) => updateDraft(item, { internalNote: event.target.value })} value={draft.internalNote ?? ""} />
          </Field>
          <Field id={`paypal-status-${item.id}`} label={l("Observed provider status", "관찰한 provider 상태")} required>
            <NativeSelect onChange={(event) => updateDraft(item, { observedProviderStatus: event.target.value as Exclude<PayPalDisputeProviderStatus, "UNKNOWN"> })} value={draft.observedProviderStatus}>
              <NativeSelectOption value="OPEN">{invariantContent("OPEN")}</NativeSelectOption>
              <NativeSelectOption value="WAITING_FOR_SELLER_RESPONSE">{invariantContent("WAITING_FOR_SELLER_RESPONSE")}</NativeSelectOption>
              <NativeSelectOption value="WAITING_FOR_BUYER_RESPONSE">{invariantContent("WAITING_FOR_BUYER_RESPONSE")}</NativeSelectOption>
              <NativeSelectOption value="UNDER_REVIEW">{invariantContent("UNDER_REVIEW")}</NativeSelectOption>
              <NativeSelectOption value="RESOLVED">{invariantContent("RESOLVED")}</NativeSelectOption>
            </NativeSelect>
          </Field>
          {draft.observedProviderStatus === "RESOLVED" ? <Field id={`paypal-outcome-${item.id}`} label={l("Observed outcome", "관찰한 결과")} required>
            <NativeSelect onChange={(event) => updateDraft(item, { observedOutcome: event.target.value as Draft["observedOutcome"] })} value={draft.observedOutcome ?? "NONE"}>
              <NativeSelectOption value="NONE">{invariantContent("NONE")}</NativeSelectOption>
              <NativeSelectOption value="RESOLVED_BUYER_FAVOUR">{invariantContent("RESOLVED_BUYER_FAVOUR")}</NativeSelectOption>
              <NativeSelectOption value="RESOLVED_SELLER_FAVOUR">{invariantContent("RESOLVED_SELLER_FAVOUR")}</NativeSelectOption>
              <NativeSelectOption value="RESOLVED_WITH_PAYOUT">{invariantContent("RESOLVED_WITH_PAYOUT")}</NativeSelectOption>
              <NativeSelectOption value="CANCELED_BY_BUYER">{invariantContent("CANCELED_BY_BUYER")}</NativeSelectOption>
              <NativeSelectOption value="ACCEPTED">{invariantContent("ACCEPTED")}</NativeSelectOption>
              <NativeSelectOption value="DENIED">{invariantContent("DENIED")}</NativeSelectOption>
            </NativeSelect>
          </Field> : null}
          <Field id={`paypal-evidence-source-${item.id}`} label={l("Evidence source", "증거 출처")} required>
            <NativeSelect onChange={(event) => updateDraft(item, { evidenceSource: event.target.value as Draft["evidenceSource"] })} value={draft.evidenceSource}>
              <NativeSelectOption value="PAYPAL_RESOLUTION_CENTER">{invariantContent("PAYPAL_RESOLUTION_CENTER")}</NativeSelectOption>
              <NativeSelectOption value="PAYPAL_EMAIL">{invariantContent("PAYPAL_EMAIL")}</NativeSelectOption>
              <NativeSelectOption value="INTERNAL_ORDER_RECORD">{invariantContent("INTERNAL_ORDER_RECORD")}</NativeSelectOption>
              <NativeSelectOption value="CARRIER">{invariantContent("CARRIER")}</NativeSelectOption>
              <NativeSelectOption value="MERCHANT_RECEIPT">{invariantContent("MERCHANT_RECEIPT")}</NativeSelectOption>
              <NativeSelectOption value="OTHER">{invariantContent("OTHER")}</NativeSelectOption>
            </NativeSelect>
          </Field>
          <Field error={draft.evidenceHash && !evidenceHashPattern.test(draft.evidenceHash.trim().toLowerCase()) ? l("Use a 0x-prefixed lowercase SHA-256 hash.", "0x로 시작하는 소문자 SHA-256 hash를 입력하세요.") : undefined} id={`paypal-evidence-hash-${item.id}`} label={l("Evidence SHA-256", "증거 SHA-256")} required>
            <Input maxLength={66} onChange={(event) => updateDraft(item, { evidenceHash: event.target.value })} placeholder={invariantContent("0x…")} required value={draft.evidenceHash} />
          </Field>
          <Field id={`paypal-observed-at-${item.id}`} label={l("Observed at", "관찰 시각")} required>
            <Input onChange={(event) => updateDraft(item, { observedAt: event.target.value })} required type="datetime-local" value={draft.observedAt} />
          </Field>
          <Button busy={busy === item.id} disabled={!valid || Boolean(busy)} emphasis="primary" onClick={() => void submit(item)} type="button">{l("Record Resolution Center action", "Resolution Center 행동 기록")}</Button>
        </div> : null}
      </article>;
    })}
  </section>;
}

function initialDraft(item: PayPalDisputeCase): Draft {
  return {
    actionKind: "CASE_OBSERVED",
    externalReference: item.disputeId,
    publicRationale: "",
    internalNote: "",
    observedProviderStatus: item.providerStatus === "UNKNOWN" ? "OPEN" : item.providerStatus,
    observedOutcome: item.outcome,
    evidenceSource: "PAYPAL_RESOLUTION_CENTER",
    evidenceHash: "",
    observedAt: localDateTime(new Date()),
  };
}

function localDateTime(value: Date) {
  const offset = value.getTimezoneOffset() * 60_000;
  return new Date(value.getTime() - offset).toISOString().slice(0, 16);
}
