import { useLocale } from "../../../../shared/i18n";
import type { CatalogOperationUsage as OperationUsage } from "../domain/catalogOperationUsage";

export function CatalogOperationUsage({ rows }: { rows?: OperationUsage[] }) {
  const { l } = useLocale();
  const labels: Record<string, string> = {
    SEARCH: l("Product search", "상품 검색"),
    DETAIL: l("Product details", "상품 상세"),
    USAGE: l("Quota checks", "한도 조회"),
    FEED: l("Background feed", "백그라운드 피드"),
    FEED_PAGE: l("Feed pages", "피드 페이지"),
    LINK_RESOLVE: l("Product link resolution", "상품 링크 확인"),
  };
  return <>{rows?.map(row => <div key={row.operation}>
    <dt>{l("{operation} (24h)", "{operation} (24시간)", { operation: labels[row.operation] ?? l("Other calls", "기타 호출") })}</dt>
    <dd>{row.localDailyLimit !== undefined ? l("{calls} calls / {limit} daily limit · {failures} failed", "{calls}회 / 일 한도 {limit}회 · 실패 {failures}회", { calls: row.requests24h, limit: row.localDailyLimit, failures: row.failures24h }) : l("{calls} calls · {failures} failed", "{calls}회 · 실패 {failures}회", { calls: row.requests24h, failures: row.failures24h })}</dd>
  </div>)}</>;
}
