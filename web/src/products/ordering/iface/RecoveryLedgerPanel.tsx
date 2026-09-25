import { useCallback, useEffect, useState } from "react";
import { Button, FeedbackState, Input, NativeSelect, NativeSelectOption, Notice } from "../../../shared/ui";
import { useLocale, type Localize } from "../../../shared/i18n";
import {
  createRecoveryEntry,
  deleteRecoveryEntry,
  listRecoveryEntries,
  recordRecoveryEntry,
  waiveRecoveryEntry,
  type RecoveryEntry,
  type RecoverySurface,
} from "../infra/agencyOrderOperatorApi";
import { formatAccountingMoney } from "./OrderAccountingPanel";

// 회수 기입(운영정합 5차 PR-D) — 운영자가 주문 번호를 짚어 그 주문의 회수
// 원장(자동 entry 포함)을 보고 MO별로 수취를 기입한다. 상태는 서버가 금액에서
// 파생하고, 자동 entry는 삭제 대신 포기로 닫는다. 금액의 집은 이 원장 하나 —
// funding의 "회수" 합계가 이 수취 SUM이다.

function causeLabel(cause: RecoveryEntry["cause"], l: Localize) {
  return {
    CHARGE_WITHOUT_ORDER: l("Charge without an order", "주문 없는 청구"),
    MERCHANT_CANCEL: l("Merchant cancellation", "상점 취소"),
    RETURN: l("Physical return", "실물 회수"),
    COST_ADJUSTMENT: l("Cost adjustment", "가격 조정"),
    OTHER: l("Other", "기타"),
  }[cause];
}

function stateLabel(state: RecoveryEntry["state"], l: Localize) {
  return {
    EXPECTED: l("Expected", "기대 중"),
    RECEIVED: l("Received", "수취"),
    OVER_RECOVERED: l("Over-recovered", "초과 수취"),
    WAIVED: l("Waived", "포기"),
    LOSS: l("Loss", "손실"),
  }[state];
}

// 운영자는 달러 단위("12.34")로 입력한다 — minor 변환 실패는 null.
function parseMoneyMinor(value: string): number | null {
  const trimmed = value.trim();
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return null;
  const [whole, cents = ""] = trimmed.split(".");
  return Number(whole) * 100 + Number((cents + "00").slice(0, 2));
}

export function RecoveryLedgerPanel({ initialOrderId }: { initialOrderId?: string }) {
  const { l } = useLocale();
  const [referenceId, setReferenceId] = useState(initialOrderId ?? "");
  const [surface, setSurface] = useState<RecoverySurface>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState<string>();
  const [receivedDrafts, setReceivedDrafts] = useState<Record<string, string>>({});
  const [draft, setDraft] = useState({ merchantOrderId: "", cause: "MERCHANT_CANCEL" as RecoveryEntry["cause"], expected: "", received: "", note: "" });

  const load = useCallback(async (target: string) => {
    if (!target.trim()) return;
    setLoading(true);
    setError(undefined);
    try {
      const response = await listRecoveryEntries(target.trim());
      setSurface(response.surface);
      setDraft((current) => ({ ...current, merchantOrderId: response.surface.merchantOrders[0]?.id ?? "" }));
    } catch (caught) {
      setSurface(undefined);
      setError(caught instanceof Error ? caught.message : l("We couldn't load the recovery ledger.", "회수 원장을 불러오지 못했습니다."));
    } finally {
      setLoading(false);
    }
  }, [l]);

  useEffect(() => {
    if (initialOrderId) void load(initialOrderId);
  }, [initialOrderId, load]);

  async function run(key: string, action: () => Promise<void>) {
    setBusy(key);
    setError(undefined);
    try {
      await action();
      if (surface) await load(surface.focusMerchantOrderId ?? surface.agencyOrderId);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : l("We couldn't process the request.", "요청을 처리하지 못했습니다."));
    } finally {
      setBusy(undefined);
    }
  }

  const moLabel = (merchantOrderId: string) => {
    const mo = surface?.merchantOrders.find((candidate) => candidate.id === merchantOrderId);
    return mo
      ? l("{shop} · checkout {ordinal}", "{shop} · checkout {ordinal}", {
          shop: mo.shopDomain,
          ordinal: mo.checkoutOrdinal,
        })
      : merchantOrderId.slice(0, 8);
  };

  return <section className="funding-recovery" aria-label={l("Record recovery", "회수 기입")}>
    <form className="funding-recovery__lookup" onSubmit={(event) => { event.preventDefault(); void load(referenceId); }}>
      <label>{l("Order ID or MO ID", "주문 ID 또는 MO ID")}<Input aria-label={l("Order ID or MO ID", "주문 ID 또는 MO ID")} placeholder={l("AgencyOrder or MerchantOrder UUID", "AgencyOrder 또는 MerchantOrder UUID")} value={referenceId} onChange={(event) => setReferenceId(event.target.value)} /></label>
      <Button type="submit" emphasis="primary" busy={loading}>{l("View recoveries", "회수 목록 조회")}</Button>
    </form>
    {error ? <Notice announce tone="danger">{error}</Notice> : null}
    {surface ? <>
      {surface.matchedBy === "MERCHANT_ORDER" ? <p className="vt-field__hint">{l("Opened the parent order's full ledger; the matching MerchantOrder row is highlighted.", "상위 주문의 전체 원장을 열었고 일치한 MerchantOrder 행을 강조합니다.")}</p> : null}
      {surface.entries.length === 0 ? <FeedbackState state="empty" title={l("This order has no recovery records", "이 주문의 회수 기록이 없습니다")} description={l("No record was created automatically by a return decision or delayed cancellation, and none has been entered manually. You can add one below.", "Return 처분·지연 취소가 자동으로 만든 기록이 없고, 수동 기입도 아직 없습니다. 아래에서 추가할 수 있습니다.")} /> : (
        <ul className="funding-recovery__entries">
          {surface.entries.map((entry) => {
            const receivedDraft = receivedDrafts[entry.id] ?? "";
            const receivedMinor = parseMoneyMinor(receivedDraft);
            return <li key={entry.id} className={`funding-recovery__entry is-${entry.state.toLowerCase()}${surface.focusMerchantOrderId === entry.merchantOrderId ? " is-focused" : ""}`}>
              <header>
                <strong>{causeLabel(entry.cause, l)}</strong>
                <span className="funding-recovery__state">{stateLabel(entry.state, l)}</span>
                <small>{moLabel(entry.merchantOrderId)} · {entry.manual ? l("Manual entry", "수동 기입") : l("Automatic record", "자동 기록")}</small>
              </header>
              <dl>
                <div><dt>{l("Expected", "기대")}</dt><dd>{formatAccountingMoney(entry.expectedAmountMinor)}</dd></div>
                <div><dt>{l("Received", "수취")}</dt><dd>{formatAccountingMoney(entry.receivedAmountMinor)}</dd></div>
              </dl>
              {entry.note ? <p>{entry.note}</p> : null}
              <div className="funding-recovery__actions">
                <label>{l("Amount received", "실제 수취액")}<Input aria-label={l("Amount received for {cause}", "{cause} 수취액", { cause: causeLabel(entry.cause, l) })} placeholder={l("Example: 12.34 (USD)", "예: 12.34 (USD)")} value={receivedDraft} onChange={(event) => setReceivedDrafts((current) => ({ ...current, [entry.id]: event.target.value }))} /></label>
                <Button size="compact" disabled={receivedMinor === null} busy={busy === `${entry.id}:record`} onClick={() => void run(`${entry.id}:record`, async () => { await recordRecoveryEntry(entry.id, receivedMinor ?? 0, "", entry.version); setReceivedDrafts((current) => ({ ...current, [entry.id]: "" })); })}>{l("Record receipt", "수취 기입")}</Button>
                {entry.state !== "WAIVED" ? <Button size="compact" emphasis="quiet" busy={busy === `${entry.id}:waive`} onClick={() => void run(`${entry.id}:waive`, async () => { await waiveRecoveryEntry(entry.id, "", entry.version); })}>{l("Close as waived", "포기로 닫기")}</Button> : null}
                {entry.manual ? <Button size="compact" emphasis="quiet" busy={busy === `${entry.id}:delete`} onClick={() => void run(`${entry.id}:delete`, async () => { await deleteRecoveryEntry(entry.id, entry.version); })}>{l("Delete", "삭제")}</Button> : null}
              </div>
            </li>;
          })}
        </ul>
      )}
      <form className="funding-recovery__create" onSubmit={(event) => {
        event.preventDefault();
        const expectedMinor = draft.expected.trim() === "" ? 0 : parseMoneyMinor(draft.expected);
        const receivedMinor = draft.received.trim() === "" ? 0 : parseMoneyMinor(draft.received);
        if (expectedMinor === null || receivedMinor === null || !draft.merchantOrderId) return;
        void run("create", async () => {
          await createRecoveryEntry({
            merchantOrderId: draft.merchantOrderId, cause: draft.cause,
            expectedAmountMinor: expectedMinor, receivedAmountMinor: receivedMinor,
            note: draft.note.trim(),
          });
          setDraft((current) => ({ ...current, expected: "", received: "", note: "" }));
        });
      }}>
        <h3>{l("Add a recovery record", "회수 기록 추가")}</h3>
        <p>{l("Enter money returned by the merchant without a physical return, such as a merchant cancellation or price adjustment.", "실물 반송 없이 상점이 돌려주는 돈(상점 취소·가격 조정 등)을 여기서 직접 기입합니다.")} {l("Status is derived from the amounts you enter.", "상태는 입력한 금액에서 자동으로 정해집니다.")}</p>
        <div className="funding-recovery__create-grid">
          <label>{l("Merchant order", "대상 MO")}<NativeSelect aria-label={l("Merchant order", "대상 MO")} value={draft.merchantOrderId} onChange={(event) => setDraft((current) => ({ ...current, merchantOrderId: event.target.value }))}>{surface.merchantOrders.map((mo) => <NativeSelectOption key={mo.id} value={mo.id}>{l("{shop} · checkout {ordinal}", "{shop} · checkout {ordinal}", { shop: mo.shopDomain, ordinal: mo.checkoutOrdinal })}</NativeSelectOption>)}</NativeSelect></label>
          <label>{l("Cause", "원인")}<NativeSelect aria-label={l("Recovery cause", "회수 원인")} value={draft.cause} onChange={(event) => setDraft((current) => ({ ...current, cause: event.target.value as RecoveryEntry["cause"] }))}><NativeSelectOption value="MERCHANT_CANCEL">{l("Merchant cancellation", "상점 취소")}</NativeSelectOption><NativeSelectOption value="COST_ADJUSTMENT">{l("Cost adjustment", "가격 조정")}</NativeSelectOption><NativeSelectOption value="RETURN">{l("Physical return", "실물 회수")}</NativeSelectOption><NativeSelectOption value="OTHER">{l("Other", "기타")}</NativeSelectOption></NativeSelect></label>
          <label>{l("Amount expected", "돌려받을 금액")}<Input aria-label={l("Amount expected", "돌려받을 금액")} placeholder={l("Example: 30.00 (USD, optional)", "예: 30.00 (USD, 비워도 됨)")} value={draft.expected} onChange={(event) => setDraft((current) => ({ ...current, expected: event.target.value }))} /></label>
          <label>{l("Amount already received", "이미 받은 금액")}<Input aria-label={l("Amount already received", "이미 받은 금액")} placeholder={l("Example: 30.00 (USD, optional)", "예: 30.00 (USD, 비워도 됨)")} value={draft.received} onChange={(event) => setDraft((current) => ({ ...current, received: event.target.value }))} /></label>
          <label className="funding-recovery__note">{l("Note", "메모")}<Input aria-label={l("Recovery note", "회수 메모")} placeholder={l("Example: Merchant issued a partial refund because the item was out of stock", "예: 상점이 품절로 부분 환불")} value={draft.note} onChange={(event) => setDraft((current) => ({ ...current, note: event.target.value }))} /></label>
        </div>
        <Button type="submit" emphasis="secondary" busy={busy === "create"} disabled={!draft.merchantOrderId}>{l("Add record", "기록 추가")}</Button>
      </form>
    </> : null}
  </section>;
}
