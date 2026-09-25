import { useCallback, useEffect, useState } from "react";
import { useLocale, type Localize } from "../../../../shared/i18n";
import { Button } from "../../../../shared/ui";
import { jobStepLabel } from "../../domain/jobPresentation";
import { sourceLabel } from "../../domain/sourceLabels";
import { getResearchRoundSummary, type ResearchRoundSummary as Summary } from "../infra/roundSummaryApi";
import "./research-round-summary.css";

function roundStatusLabel(status: string, l: Localize) {
  return {
    RESULTS_READY: l("Candidates found", "후보 확보"),
    NO_RESULTS: l("No new candidates", "새 후보 없음"),
    FAILED: l("Failed", "실패"),
    CANCELLED: l("Cancelled", "취소"),
    SUPERSEDED: l("Superseded", "대체됨"),
    REQUESTED: l("In progress", "진행 중"),
  }[status] ?? status;
}
function countryLabel(country: string, l: Localize) {
  return { KR: l("Korea", "한국"), US: l("United States", "미국") }[country] ?? (country || l("Unknown", "미확인"));
}
function coverageLabel(status: string, l: Localize) {
  return {
    SUCCEEDED: l("Succeeded", "성공"), EMPTY: l("Empty", "결과 없음"), PARTIAL: l("Partial", "부분"),
    FAILED: l("Failed", "실패"), SKIPPED: l("Skipped", "건너뜀"), UNSUPPORTED: l("Unsupported", "미지원"),
    DEFERRED: l("Deferred", "지연"), STARTED: l("Started", "시작"),
  }[status] ?? status;
}
function stepLabel(kind: string, l: Localize) {
  if (kind === "INTERPRETING" || kind === "SEARCHING_CATALOG" || kind === "RANKING" || kind === "SUBMITTING") return jobStepLabel(kind, l);
  return l("Not recorded", "기록 없음");
}
function attemptStatusLabel(status: string, l: Localize) {
  return {
    RUNNING: l("Running", "진행 중"), SUCCEEDED: l("Succeeded", "성공"), FAILED: l("Failed", "실패"),
    CANCELLED: l("Cancelled", "취소"), DEFERRED: l("Deferred", "지연"), EFFECT_UNKNOWN: l("Result unconfirmed", "결과 미확인"),
  }[status] ?? status;
}
function reservationStatusLabel(status: string, l: Localize) {
  return {
    HELD: l("Held", "보유 중"), SETTLED: l("Settled", "정산"), RELEASED: l("Released", "반환"),
    UNKNOWN: l("Unknown · still counted today", "미확인 · 당일 한도 유지"),
  }[status] ?? status;
}
const seconds = (value: number) => `${value.toFixed(1)}s`;
const usd = (micros: number) => `$${(micros / 1e6).toFixed(4)}`;

/**
 * Operator read model over the durable Round ledgers. Every refresh reads
 * tables only; it never spends a provider call or a model call.
 */
export function ResearchRoundSummary({ days = 7 }: { days?: number }) {
  const { l } = useLocale();
  const [summary, setSummary] = useState<Summary>();
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    setBusy(true); setError(false);
    try { setSummary(await getResearchRoundSummary(days)); } catch { setError(true); } finally { setBusy(false); }
  }, [days]);
  useEffect(() => { void load(); }, [load]);
  const empty = <p className="research-round-summary__empty">{l("No records in the last {days} days.", "지난 {days}일 동안 기록이 없습니다.", { days })}</p>;
  return <section className="research-round-summary" aria-label={l("Research rounds", "조사 라운드")}>
    <h2>{l("Research rounds · last {days} days", "조사 라운드 · 최근 {days}일")}</h2>
    <p>{l("Round outcomes, failure reasons with the step that stopped, per-source coverage, the provider call ledger, stage durations, attempt outcomes, model budget reservations and how many candidates each round admitted. Reading this never calls a provider.", "라운드 결과, 중단 단계별 실패 사유, 소스별 coverage, 제공사 호출 원장, 단계 소요 시간, 시도 결과, 모델 예산 예약, 라운드당 확보 후보 수입니다. 이 화면은 제공사를 호출하지 않습니다.")}</p>
    {error && <p role="alert">{l("The round summary could not be loaded. Try again.", "라운드 요약을 불러오지 못했습니다. 다시 시도해 주세요.")}</p>}
    {summary && <div className="research-round-summary__grid">
      <section className="research-round-summary__panel" aria-label={l("Round outcomes", "라운드 결과")}>
        <h3>{l("Round outcomes", "라운드 결과")}</h3>
        {summary.rounds.length === 0 ? empty : <table><thead><tr><th>{l("Country", "국가")}</th><th>{l("Outcome", "결과")}</th><th>{l("Rounds", "라운드")}</th></tr></thead>
          <tbody>{summary.rounds.map(row => <tr key={`${row.country}:${row.status}`}><td>{countryLabel(row.country, l)}</td><td>{roundStatusLabel(row.status, l)}</td><td>{row.count}</td></tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Failure reasons", "실패 사유")}>
        <h3>{l("Failure reasons", "실패 사유")}</h3>
        {summary.failures.length === 0 ? empty : <table><thead><tr><th>{l("Code", "코드")}</th><th>{l("Stopped at", "중단 단계")}</th><th>{l("Retryable", "재시도")}</th><th>{l("Rounds", "라운드")}</th></tr></thead>
          <tbody>{summary.failures.map(row => <tr key={`${row.failureCode}:${row.stepKind}:${row.retryable}`}><td><span className="research-round-summary__code">{row.failureCode}</span></td><td>{stepLabel(row.stepKind, l)}</td><td>{row.retryable ? l("Yes", "가능") : l("No", "불가")}</td><td>{row.count}</td></tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Source coverage", "소스 coverage")}>
        <h3>{l("Source coverage", "소스 coverage")}</h3>
        {summary.sources.length === 0 ? empty : <table><thead><tr><th>{l("Country", "국가")}</th><th>{l("Source", "소스")}</th><th>{l("Status", "상태")}</th><th>{l("Reason", "사유")}</th><th>{l("Rounds", "라운드")}</th></tr></thead>
          <tbody>{summary.sources.map(row => <tr key={`${row.country}:${row.source}:${row.status}:${row.reasonCode}`}><td>{countryLabel(row.country, l)}</td><td>{sourceLabel(row.source, l)}</td><td>{coverageLabel(row.status, l)}</td><td><span className="research-round-summary__code">{row.reasonCode || "—"}</span></td><td>{row.count}</td></tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Research route decisions", "조사 경로 판단")}>
        <h3>{l("Research route decisions", "조사 경로 판단")}</h3>
        {!summary.routes?.length ? empty : <table><thead><tr>
          <th>{l("Route / policy", "경로 / 정책")}</th><th>{l("Category", "상품군")}</th><th>{l("Outcome / reason", "결과 / 사유")}</th><th>{l("Pressure", "포화도")}</th><th>{l("Count", "횟수")}</th>
        </tr></thead><tbody>{summary.routes.map(row => <tr key={`${row.routeId}:${row.policyVersion}:${row.productVertical}:${row.decision}:${row.reason}`}>
          <td>{row.routeId}<br />{row.policyVersion}</td><td>{row.productVertical}</td><td>{coverageLabel(row.decision,l)} {row.reason}</td><td>{Math.round(row.meanPressure*100)}%</td><td>{row.count}</td>
        </tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Provider calls", "제공사 호출")}>
        <h3>{l("Provider calls", "제공사 호출")}</h3>
        {summary.apiCalls.length === 0 ? empty : <table><thead><tr><th>{l("API", "API")}</th><th>{l("Outcome", "결과")}</th><th>{l("Billable", "과금")}</th><th>{l("Calls", "호출")}</th></tr></thead>
          <tbody>{summary.apiCalls.map(row => <tr key={`${row.apiId}:${row.outcome}:${row.billable}`}><td><span className="research-round-summary__code">{row.apiId}</span></td><td><span className="research-round-summary__code">{row.outcome}</span></td><td>{row.billable ? l("Upstream", "제공사") : l("Local denial", "로컬 거절")}</td><td>{row.count}</td></tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Stage durations", "단계 소요 시간")}>
        <h3>{l("Stage durations", "단계 소요 시간")}</h3>
        {!summary.steps?.length ? empty : <table><thead><tr><th>{l("Stage", "단계")}</th><th>{l("Completed", "완료")}</th><th>{l("Median", "중앙값")}</th><th>{l("95th percentile", "95%")}</th></tr></thead>
          <tbody>{summary.steps.map(row => <tr key={row.kind}><td>{stepLabel(row.kind, l)}</td><td>{row.count}</td><td>{seconds(row.p50Seconds)}</td><td>{seconds(row.p95Seconds)}</td></tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Attempt outcomes", "시도 결과")}>
        <h3>{l("Attempt outcomes", "시도 결과")}</h3>
        {!summary.attempts?.length ? empty : <table><thead><tr><th>{l("Status", "상태")}</th><th>{l("Code", "코드")}</th><th>{l("Attempts", "시도")}</th></tr></thead>
          <tbody>{summary.attempts.map(row => <tr key={`${row.status}:${row.failureCode}`}><td>{attemptStatusLabel(row.status, l)}</td><td><span className="research-round-summary__code">{row.failureCode || "—"}</span></td><td>{row.count}</td></tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Model budget reservations", "모델 예산 예약")}>
        <h3>{l("Model budget reservations", "모델 예산 예약")}</h3>
        {!summary.reservations?.length ? empty : <table><thead><tr><th>{l("Status", "상태")}</th><th>{l("Reservations", "건수")}</th><th>{l("Amount", "금액")}</th></tr></thead>
          <tbody>{summary.reservations.map(row => <tr key={row.status}><td>{reservationStatusLabel(row.status, l)}</td><td>{row.count}</td><td>{usd(row.amountMicros)}</td></tr>)}</tbody></table>}
      </section>
      <section className="research-round-summary__panel" aria-label={l("Admitted candidates per round", "라운드당 확보 후보")}>
        <h3>{l("Admitted candidates per round", "라운드당 확보 후보")}</h3>
        {summary.admitted.rounds === 0 ? empty : <table><thead><tr><th>{l("Candidates", "후보 수")}</th><th>{l("Rounds", "라운드")}</th></tr></thead>
          <tbody>{summary.admitted.buckets.map(bucket => <tr key={bucket.label}><td>{bucket.label}</td><td>{bucket.count}</td></tr>)}
            <tr><td>{l("Median", "중앙값")}</td><td>{summary.admitted.median}</td></tr>
            <tr><td>{l("Evaluated / not evaluated", "평가 / 미평가")}</td><td>{summary.evaluation.evaluated} / {summary.evaluation.unevaluated}</td></tr></tbody></table>}
      </section>
    </div>}
    <div className="research-round-summary__actions">
      <Button emphasis="quiet" size="compact" disabled={busy} onClick={() => void load()}>{l("Refresh summary", "요약 새로고침")}</Button>
    </div>
  </section>;
}
