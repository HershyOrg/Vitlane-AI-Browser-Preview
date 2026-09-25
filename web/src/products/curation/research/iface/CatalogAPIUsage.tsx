import { useEffect, useState } from "react";
import { request } from "../../../../shared/api/client";
import { useLocale } from "../../../../shared/i18n";
import { Button } from "../../../../shared/ui";

import { CatalogAPIRow } from "./CatalogAPIRow";
import { AmazonAPIUsage } from "./AmazonAPIUsage";

import { CatalogOperationUsage } from "./CatalogOperationUsage";
import type { CatalogOperationUsage as OperationUsage } from "../domain/catalogOperationUsage";

type ResourceState = {
  canStart: boolean; pressure: number; costClass: string; reason?: string; readyAt?: string;
  constraints: Array<{id: string; kind: string; used: number; limit: number; pressure: number; reason?: string; readyAt?: string}>;
};
type Usage = { operations24h?: OperationUsage[];  resources?: ResourceState; localMaxConcurrent?: number; id: string; apiProvider: string; apiProduct: string; quotaScope: string; configured: boolean; control: { enabled: boolean; version: number }; localRequestsPerMinute: number; localDailyLimit: number; requests24h: number; notFound24h?: number; estimatedRemaining?: number; quota?: { limit: number; remaining: number; observedAt: string; resetAt: string }; failures24h: Array<{ reasonCode: string; count: number }> };
export function CatalogAPIUsage({ includeAmazon = false }: { includeAmazon?: boolean }) {
  const { l } = useLocale();
  const [rows, setRows] = useState<Usage[]>([]);
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState(false);
  useEffect(() => { let active = true; void request<{ apis: Usage[] }>("/api/v1/admin/catalog-apis").then((value) => { if (active) setRows(value.apis); }).catch(() => { if (active) setError(true); }); return () => { active = false; }; }, []);
  async function update(row: Usage, toggle: boolean) {
    setBusy(row.id); setError(false);
    const path = `/api/v1/admin/catalog-apis/${encodeURIComponent(row.id)}`;
    try {
      const result = await request<Usage>(`${path}/${toggle ? "control" : "usage?refresh=true"}`, toggle ? { method: "PUT", body: JSON.stringify({ schemaVersion: "vitlane.catalog-api-control.v1", enabled: !row.control.enabled, expectedVersion: row.control.version }) } : {});
      setRows((current) => current.map((item) => item.id === row.id ? result : item));
      // Shared account changes must be reflected in every Actor's composed view.
      const all = await request<{ apis: Usage[] }>("/api/v1/admin/catalog-apis");
      setRows(all.apis);
    } catch {
      setError(true);
      try { const result = await request<Usage>(`${path}/usage`); setRows((current) => current.map((item) => item.id === row.id ? result : item)); } catch { /* Keep the last confirmed value. */ }
    } finally { setBusy(undefined); }
  }
  const providers = [...new Set([...(includeAmazon ? ["OpenWebNinja"] : []), ...rows.map(row => row.apiProvider)])];
  return <section className="catalog-api-usage" aria-label={l("Product research APIs", "상품 조사 API")}>
    <h2>{l("Product research APIs", "상품 조사 API")}</h2>
    {error && <p role="alert">{l("The API setting or usage could not be confirmed. Review the saved state and try again.", "API 설정이나 사용량을 확인하지 못했습니다. 저장된 상태를 확인한 후 다시 시도해 주세요.")}</p>}
    <div className="catalog-api-usage__head" aria-hidden="true"><span>{l("Provider", "제공자")}</span><div><span>{l("API", "API")}</span><span>{l("Usage (24h)", "사용량 (24시간)")}</span><span>{l("Access", "사용 설정")}</span></div></div>
    {providers.map(provider => <section className="catalog-api-group" key={provider} aria-label={provider}>
      <h3>{provider}</h3><div className="catalog-api-group__rows">
      {includeAmazon && provider === "OpenWebNinja" && <AmazonAPIUsage />}
      {rows.filter(row => row.apiProvider === provider).map(row => <CatalogAPIRow key={row.id} name={row.apiProduct}
        enabled={row.control.enabled} disabled={Boolean(busy) || (!row.configured && !row.control.enabled)} onToggle={() => void update(row, true)}
        requests={row.requests24h} remaining={row.quota?.remaining}
        notices={!row.configured ? <p>{l("Server configuration required", "서버 설정 필요")}</p> : undefined}>
        <dl><div><dt>{l("Shared quota scope", "공유 한도 범위")}</dt><dd>{row.quotaScope}</dd></div>
          <div><dt>{l("Reported remaining / limit", "제공사 잔여량 / 한도")}</dt><dd>{row.quota ? `${row.quota.remaining} / ${row.quota.limit}` : l("Unknown", "미확인")}</dd></div>
          <div><dt>{l("Estimated remaining", "추정 잔여량")}</dt><dd>{row.estimatedRemaining ?? l("Unknown", "미확인")}</dd></div>
          <div><dt>{l("Local limit: minute / 24 hours", "로컬 한도: 분 / 24시간")}</dt><dd>{row.localRequestsPerMinute} / {row.localDailyLimit}</dd></div>
          <div><dt>{l("Concurrent calls", "동시 호출 한도")}</dt><dd>{row.localMaxConcurrent ?? l("Unknown", "미확인")}</dd></div>
          {row.resources && <>
            <div><dt>{l("Resource pressure", "자원 포화도")}</dt><dd>{Math.round(row.resources.pressure * 100)}%</dd></div>
            <div><dt>{l("Admission", "호출 가능 상태")}</dt><dd>{row.resources.canStart ? l("Available", "사용 가능") : row.resources.reason}</dd></div>
            <div><dt>{l("Billing", "과금 방식")}</dt><dd>{row.resources.costClass === "FREE" ? l("Free", "무료") : row.resources.costClass === "INCLUDED" ? l("Included quota", "구독 포함량") : l("Metered", "종량 과금")}</dd></div>
            {row.resources.readyAt && <div><dt>{l("Retry after", "재시도 가능 시각")}</dt><dd>{new Date(row.resources.readyAt).toLocaleString()}</dd></div>}
            {row.resources.constraints.filter(c => c.limit > 0 && c.kind !== "CONTROL").map(c => <div key={c.id}><dt>{c.id}</dt><dd>{c.kind === "BUDGET" ? `USD ${(c.used / 1_000_000).toFixed(4)} / ${(c.limit / 1_000_000).toFixed(2)}` : `${c.used} / ${c.limit}`} · {Math.round(c.pressure * 100)}%</dd></div>)}
          </>}
          <div><dt>{l("Quota observed at", "한도 확인 시각")}</dt><dd>{row.quota ? new Date(row.quota.observedAt).toLocaleString() : l("Unknown", "미확인")}</dd></div>
          <div><dt>{l("Quota resets at", "한도 초기화 시각")}</dt><dd>{row.quota ? new Date(row.quota.resetAt).toLocaleString() : l("Unknown", "미확인")}</dd></div>
          <div><dt>{l("Not found (24h)", "미발견 (24시간)")}</dt><dd>{row.notFound24h ?? 0}</dd></div>
        <CatalogOperationUsage rows={row.operations24h} />
        </dl>
        <ul>{row.failures24h.map(failure => <li key={failure.reasonCode}>{failure.reasonCode}: {failure.count}</li>)}</ul>
        <p>{l("Switches control new calls. Local limits are Vitlane safeguards; unreported provider quota stays unknown. Not found counts completed calls that returned no product, such as discontinued 11st product numbers, and is not a failure.", "스위치는 신규 호출을 제어합니다. 로컬 한도는 Vitlane 보호값이며 제공사 미보고 잔여량은 미확인입니다. 미발견은 호출은 완료했지만 상품이 없던 건수로, 판매 중지된 11번가 상품번호가 대표적이며 실패로 세지 않습니다.")}</p>
        <Button emphasis="quiet" size="compact" disabled={Boolean(busy) || !row.configured || !row.control.enabled} onClick={() => void update(row, false)}>{l("Refresh usage", "사용량 새로고침")}</Button>
      </CatalogAPIRow>)}
      </div>
    </section>)}
  </section>;
}
