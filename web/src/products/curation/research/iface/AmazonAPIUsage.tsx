import { useEffect, useState } from "react";
import { Button } from "../../../../shared/ui";
import { useLocale } from "../../../../shared/i18n";
import { CatalogAPIRow } from "./CatalogAPIRow";
import { amazonUsage, setAmazonControl, type AmazonUsage } from "../infra/amazonApi";

import { CatalogOperationUsage } from "./CatalogOperationUsage";

export function AmazonAPIUsage() {
  const { l } = useLocale();
  const [usage, setUsage] = useState<AmazonUsage>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [saving, setSaving] = useState(false);
  const [controlError, setControlError] = useState(false);
  async function load(refresh = false) {
    setBusy(true); setError(false);
    try { setUsage(await amazonUsage(refresh)); } catch { setError(true); } finally { setBusy(false); }
  }
  async function toggle() {
    if (!usage?.control || saving) return;
    setSaving(true); setControlError(false);
    try {
      const result = await setAmazonControl(!usage.control.enabled, usage.control.version);
      setUsage((previous) => previous && { ...previous, ...result, schemaVersion: previous.schemaVersion });
    } catch {
      setControlError(true);
      try { setUsage(await amazonUsage()); } catch { /* Keep the last confirmed state. */ }
    } finally { setSaving(false); }
  }
  useEffect(() => { void load(); }, []);
  const unknown = l("Unknown", "미확인");
  return <CatalogAPIRow name="Real-Time Amazon Data" enabled={Boolean(usage?.control?.enabled)}
    disabled={busy || saving || !usage?.control || (!usage.configured && !usage.control.enabled)} onToggle={() => void toggle()}
    requests={usage?.requests24h} remaining={usage?.quota?.remaining}
    notices={<>
      {usage && !usage.configured && <p>{l("Server configuration required", "서버 설정 필요")}</p>}
      {usage?.mode === "STUB" && <p role="status">{l("Local Amazon snapshot (stub). Live API calls are off; quota values are from the last live check.", "Amazon 로컬 스냅샷(stub)입니다. 실제 API 호출은 중지됐으며 잔여량은 마지막 실조회 기록입니다.")}</p>}
      {controlError && <p role="alert">{l("Couldn't save the Amazon setting. Review the current switch state and try again.", "Amazon 설정을 저장하지 못했습니다. 현재 스위치 상태를 확인한 후 다시 시도해 주세요.")}</p>}
      {error && <p role="alert">{l("Couldn't refresh usage. The last confirmed values are kept.", "사용량을 갱신하지 못했습니다. 마지막 확인값을 유지합니다.")}</p>}
    </>}>
    <Button emphasis="quiet" size="compact" disabled={busy || saving} onClick={() => void load(true)}>{l("Refresh Amazon usage", "Amazon 사용량 새로고침")}</Button>
    <p>{l("Turning this off pauses new Amazon API calls. Saved selections and purchase checks remain available. Calls already in progress may finish.", "끄면 새로운 Amazon API 호출을 중지합니다. 저장된 선택과 구매 체크는 유지되며 이미 진행 중인 호출은 완료될 수 있습니다.")}</p>
    <dl>
      <div><dt>{l("Reported remaining / limit", "제공사 잔여량 / 한도")}</dt><dd>{usage?.quota ? `${usage.quota.remaining} / ${usage.quota.limit}` : unknown}</dd></div>
      <div><dt>{l("Estimated remaining after Vitlane calls", "Vitlane 호출 반영 추정 잔여량")}</dt><dd>{usage?.estimatedRemaining ?? unknown}</dd></div>
      <div><dt>{l("Quota observed at", "한도 확인 시각")}</dt><dd>{usage?.quota ? new Date(usage.quota.observedAt).toLocaleString() : unknown}</dd></div>
      <div><dt>{l("Quota resets at", "한도 초기화 시각")}</dt><dd>{usage?.quota ? new Date(usage.quota.resetAt).toLocaleString() : unknown}</dd></div>
      <div><dt>{l("Calls / successful calls in 24 hours", "24시간 호출 / 성공")}</dt><dd>{usage ? `${usage.requests24h} / ${usage.succeeded24h}` : unknown}</dd></div>
    <CatalogOperationUsage rows={usage?.operations24h} />
    </dl>
    <p>{l("The estimate includes Vitlane attempts since the last quota check. Other clients may also use this key. Old values are not proof that calls are currently available.", "추정치는 마지막 한도 조회 이후 Vitlane 호출 시도를 반영합니다. 다른 클라이언트도 같은 키를 사용할 수 있습니다. 과거 확인값은 현재 호출 가능 여부를 보장하지 않습니다.")}</p>
    {usage?.quotaRefreshFailure ? <p role="alert">{l("Quota refresh failed: {code}", "한도 갱신 실패: {code}", { code: usage.quotaRefreshFailure })}</p> : null}
    {usage?.lastFailureCode ? <p>{l("Latest failure: {code} ({time})", "최근 실패: {code} ({time})", { code: usage.lastFailureCode, time: usage.lastFailureAt ? new Date(usage.lastFailureAt).toLocaleString() : unknown })}</p> : null}
    <ul>{usage?.failures24h?.map((failure) => <li key={failure.reasonCode}>{failure.reasonCode}: {failure.count}</li>)}</ul>
    <p>{l("AUTH / QUOTA / RATE indicate credentials or API limits. NETWORK / TIMEOUT / UPSTREAM indicate connection or upstream failures. SCHEMA / ASIN / INTERNAL indicate parsing, identity or application failures.", "AUTH / QUOTA / RATE는 인증·API 한도, NETWORK / TIMEOUT / UPSTREAM은 연결·제공사 장애, SCHEMA / ASIN / INTERNAL은 파싱·식별자·기능 오류를 뜻합니다.")}</p>
  </CatalogAPIRow>;
}
