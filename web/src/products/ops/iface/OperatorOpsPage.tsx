import { useCallback, useEffect, useMemo, useState } from "react";
import { useCurrentUser } from "../../account/app/useCurrentUser";
import { APIError } from "../../../shared/api/client";
import { Badge, Button, ButtonLink, Card, CardContent, CardDescription, CardHeader, CardTitle, Disclosure, FeedbackState, Field, Input, Notice, PageHeader, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Tabs, TabsContent, TabsList, TabsTrigger, Textarea, Selection } from "../../../shared/ui";
import {
  resolveUnknownReservation,
  type UnknownReservationOutcome,
  type UnknownReservationResolution,
} from "../../curation/intelligence/managedrunner/infra/adminUsageApi";
import {
  getActiveSessions,
  getLiveControl,
  getOpsHealth,
  getOpsHeartbeats,
  getOpsSamples,
  revokeUserSessions,
  killLiveControl,
  type ActiveSessions,
  type OpsHealth,
  type OpsHeartbeats,
  type OpsSamplePoint,
  type OpsSamples,
  type LiveControl,
  type LiveControlScope,
} from "../infra/opsApi";
import "./operator-ops.css";
import { localizeFixedCopy, readLocalePreference, useLocale, type Localize } from "../../../shared/i18n";

type TrendRange = 24 | 168 | 720;
type OpsTab = "status" | "sessions" | "cost" | "live";

type SessionGroup = {
  userId: string;
  email: string;
  operator: boolean;
  sessions: ActiveSessions["sessions"];
};

const REFRESH_INTERVAL_MS = 60_000;

// 운영 현황 대시보드 (ADR-0040 §6, Phase 7-3c): L1 Healthchecks 하트비트,
// L2 앱 readback, L3 호스트 팩트와 시계열을 운영자 한 화면에 모은다.
// 임계·경보는 여기가 아니라 호스트 watch와 Healthchecks가 소유한다.
export function OperatorOpsPage() {
  const { l } = useLocale();
  const { user } = useCurrentUser();
  const operator = Boolean(user?.marketingAdmin || user?.phase5Operator);
  const [health, setHealth] = useState<OpsHealth | null>(null);
  const [heartbeats, setHeartbeats] = useState<OpsHeartbeats | null>(null);
  const [heartbeatsError, setHeartbeatsError] = useState(false);
  const [samples, setSamples] = useState<OpsSamples | null>(null);
  const [sessions, setSessions] = useState<ActiveSessions | null>(null);
  const [liveControl, setLiveControl] = useState<LiveControl | null>(null);
  const [activeTab, setActiveTab] = useState<OpsTab>("status");
  const [range, setRange] = useState<TrendRange>(24);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshedAt, setRefreshedAt] = useState<Date | null>(null);

  const load = useCallback(
    async (nextRange: TrendRange, quiet = false) => {
      if (!operator) {
        setLoading(false);
        return;
      }
      if (!quiet) setLoading(true);
      setError(null);
      const [healthResult, heartbeatsResult, samplesResult, sessionsResult, liveResult] =
        await Promise.allSettled([
          getOpsHealth(),
          getOpsHeartbeats(),
          getOpsSamples(nextRange),
          getActiveSessions(),
          getLiveControl(),
        ]);
      if (healthResult.status === "fulfilled") {
        setHealth(healthResult.value);
      } else {
        setError(l("We couldn't load operational status. Check your connection.", "운영 상태를 불러오지 못했습니다. 연결을 확인해 주세요."));
      }
      if (heartbeatsResult.status === "fulfilled") {
        setHeartbeats(heartbeatsResult.value);
        setHeartbeatsError(false);
      } else {
        setHeartbeatsError(true);
      }
      if (samplesResult.status === "fulfilled") {
        setSamples(samplesResult.value);
      }
      if (sessionsResult.status === "fulfilled") {
        setSessions(sessionsResult.value);
      }
      if (liveResult.status === "fulfilled") {
        setLiveControl(liveResult.value.liveControl);
      } else {
        setLiveControl(null);
      }
      setRefreshedAt(new Date());
      if (!quiet) setLoading(false);
    },
    [l, operator],
  );

  useEffect(() => {
    void load(range);
  }, [load, range]);

  useEffect(() => {
    if (!operator) return;
    const timer = window.setInterval(() => {
      void load(range, true);
    }, REFRESH_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [load, operator, range]);

  if (!operator) {
    return (
      <section className="catalog-ui-admin-main">
        <PageHeader
          eyebrow={l("Access", "접근 권한")}
          title={l("You can't view operations status", "운영 현황을 볼 수 없습니다")}
          description={l("This page is available only to authorized operators.", "이 화면은 허용된 운영자에게만 열립니다.")}
        />
        <ButtonLink href="/">{l("Go to shopping home", "구매 홈으로 이동")}</ButtonLink>
      </section>
    );
  }

  return (
    <section className="catalog-ui-admin-main product-ui-ops">
      <PageHeader
        eyebrow="Limited Production"
        title={l("Operations status and actions", "운영 현황과 조치")}
        description={l("Read L1, L2, and L3 status first, then execute only necessary operational actions with reasons and evidence. Host watch and Healthchecks own alerts and thresholds.", "L1·L2·L3 상태를 먼저 읽고, 필요한 운영 조치만 사유와 증거를 남겨 실행합니다. 경보와 임계값은 호스트 watch와 Healthchecks가 소유합니다.")}
        secondaryActions={[{
          label: l("Refresh", "새로고침"),
          disabled: loading,
          onClick: () => void load(range, true),
        }]}
        summary={(
          <div className="product-ui-ops__summary">
            <span>{l("Core", "Core")} <strong>{statusLabel(health?.core.status, l)}</strong></span>
            <span>{l("Sessions", "세션")} <strong>{sessions?.sessions.length ?? 0}</strong></span>
            <span>{l("Cost review", "비용 확인")} <strong>{health?.observed?.costReservations.unknownCount ?? 0}</strong></span>
            {refreshedAt && (
              <span className="product-ui-ops__refreshed">
                {l("Updated {time} · Automatically every 60 seconds", "{time} 갱신 · 60초마다 자동", { time: formatKST(refreshedAt.toISOString()) })}
              </span>
            )}
          </div>
        )}
      />

      {loading && !health && (
        <FeedbackState
          state="loading"
          description={l("Summarizing operational status.", "운영 상태를 집계하고 있습니다.")}
        />
      )}
      {error && !health && (
        <FeedbackState
          state="error"
          title={l("We couldn't load operational status", "운영 상태를 불러오지 못했습니다")}
          description={error}
          action={{ label: l("Try again", "다시 시도"), onAction: () => void load(range) }}
        />
      )}

      {health && (
        <Tabs
          onValueChange={(value) => setActiveTab(value as OpsTab)}
          value={activeTab}
        >
          <TabsList aria-label={l("Operations view", "운영 현황 보기")} className="product-ui-tracking-tabs">
            <TabsTrigger value="status">{l("Status", "상태")}</TabsTrigger>
            <TabsTrigger value="sessions">
              {l("Sessions", "세션")} <span>{groupSessions(sessions?.sessions ?? []).length}</span>
            </TabsTrigger>
            <TabsTrigger value="cost">
              {l("Cost decisions", "비용 판정")} <span>{health.observed?.costReservations.unknownCount ?? 0}</span>
            </TabsTrigger>
            <TabsTrigger value="live">{l("PayPal Live", "PayPal Live")}</TabsTrigger>
          </TabsList>

          <TabsContent value="status">
            <div className="product-ui-ops__status-stack">
              {error && (
                <FeedbackState
                  state="error"
                  title={l("We couldn't refresh to the latest status", "최신 상태로 갱신하지 못했습니다")}
                  description={error}
                  action={{ label: l("Try again", "다시 시도"), onAction: () => void load(range, true) }}
                />
              )}
              <StatusOverview health={health} />
              <HeartbeatsCard heartbeats={heartbeats} failed={heartbeatsError} />
              <ReadbackGrid health={health} />
              <HostCard host={health.host} />
              <TrendsCard
                samples={samples}
                range={range}
                onRangeChange={setRange}
              />
            </div>
          </TabsContent>

          <TabsContent value="sessions">
            <SessionsCard
              currentUserId={user?.id}
              sessions={sessions}
              onChanged={() => void load(range, true)}
            />
          </TabsContent>

          <TabsContent value="cost">
            <UnknownReservationCard
              count={health.observed?.costReservations.unknownCount ?? 0}
              oldestAgeSeconds={health.observed?.costReservations.oldestUnknownAgeSeconds ?? 0}
              onChanged={() => void load(range, true)}
            />
          </TabsContent>

          <TabsContent value="live">
            <LiveControlCard state={liveControl} onChanged={(next) => setLiveControl(next)} />
          </TabsContent>
        </Tabs>
      )}
    </section>
  );
}

const liveConfirmation: Record<LiveControlScope, string> = {
  ALL_NEW_LIVE_EFFECTS: "KILL PAYPAL LIVE ALL",
  LIVE_ORDER_ISSUE: "KILL PAYPAL LIVE ORDERS",
  PAYPAL_LIVE_MONEY_EFFECT: "KILL PAYPAL LIVE MONEY",
  LIVE_MERCHANT_EFFECT: "KILL PAYPAL LIVE MERCHANT",
};

function LiveControlCard({ state, onChanged }: {
  state: LiveControl | null;
  onChanged: (next: LiveControl) => void;
}) {
  const { l } = useLocale();
  const [scope, setScope] = useState<LiveControlScope>("ALL_NEW_LIVE_EFFECTS");
  const [reason, setReason] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [working, setWorking] = useState(false);
  const [feedback, setFeedback] = useState<{ danger: boolean; text: string }>();
  if (!state) {
    return <FeedbackState state="error" title={l("Live control unavailable", "Live 제어 상태를 불러올 수 없습니다")} description={l("Treat new Live effects as unavailable until the server readback recovers.", "서버 상태 조회가 복구될 때까지 새 Live 효과를 중단 상태로 취급하세요.")} />;
  }
  const rows: Array<[string, LiveControl["orderIssue"]]> = [
    [l("New Live orders", "새 Live 주문"), state.orderIssue],
    [l("PayPal authorization and capture", "PayPal 승인·청구"), state.paypalMoney],
    [l("Merchant purchase start", "판매처 구매 시작"), state.merchantEffect],
  ];
  async function submitKill() {
    if (!state) return;
    setWorking(true);
    setFeedback(undefined);
    try {
      const result = await killLiveControl({ scope, reason, confirmation, expectedVersion: state.version });
      onChanged(result.liveControl);
      setReason("");
      setConfirmation("");
      setFeedback({ danger: false, text: l("The kill command was applied and audited.", "종료 명령이 반영되고 감사 기록에 남았습니다.") });
    } catch (caught) {
      const code = caught instanceof APIError ? caught.reasonCode ?? caught.code : "LIVE_CONTROL_MUTATION_FAILED";
      setFeedback({ danger: true, text: l("The kill command was not applied. ({code})", "종료 명령이 반영되지 않았습니다. ({code})", { code }) });
    } finally {
      setWorking(false);
    }
  }
  return <Card>
    <CardHeader>
      <CardTitle>{l("PayPal Live kill switch", "PayPal Live 킬스위치")}</CardTitle>
      <CardDescription>{l("This screen can only stop new Live effects. Reactivation is available only from an audited server command.", "이 화면에서는 새 Live 효과를 종료만 할 수 있습니다. 재활성화는 감사되는 서버 명령으로만 가능합니다.")}</CardDescription>
    </CardHeader>
    <CardContent className="product-ui-ops__live-control">
      <dl className="product-ui-ops__rows">
        {rows.map(([label, gate]) => <div key={label}><dt>{label}</dt><dd><Badge variant={gate.effective ? "secondary" : "destructive"}>{gate.effective ? l("Enabled", "활성") : l("Killed", "종료")}</Badge> <small>{l("static {static} · runtime {runtime}", "정적 {static} · 런타임 {runtime}", { static: gate.staticAllowed ? "ALLOW" : "DENY", runtime: gate.runtimeKilled ? "KILLED" : "OPEN" })}</small></dd></div>)}
      </dl>
      <p className="product-ui-ops__muted">{l("Version {version} · Last change {time} · {actor} · {reason}", "버전 {version} · 마지막 변경 {time} · {actor} · {reason}", { version: state.version, time: formatKST(state.changedAt), actor: state.changedBy, reason: state.reason })}</p>
      <Notice tone="danger" title={l("Fresh sign-in and exact confirmation required", "최근 재인증과 정확한 확인 문구 필요")}>{l("Killing blocks only new Live admissions. Reconciliation, webhooks, refunds, compensation, shipping, and evidence for effects already started remain available.", "종료는 새 Live 진입만 차단합니다. 대사·웹훅·환불·보상·배송과 이미 시작된 효과의 증거 기록은 계속 가능합니다.")}</Notice>
      <Field id="live-control-scope" label={l("Kill scope", "종료 범위")}>
        <Select value={scope} onValueChange={(value) => { setScope(value as LiveControlScope); setConfirmation(""); }}>
          <SelectTrigger><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="ALL_NEW_LIVE_EFFECTS">{l("All new Live effects", "모든 새 Live 효과")}</SelectItem>
            <SelectItem value="LIVE_ORDER_ISSUE">{l("New order issue", "새 주문 발행")}</SelectItem>
            <SelectItem value="PAYPAL_LIVE_MONEY_EFFECT">{l("PayPal money effects", "PayPal 자금 효과")}</SelectItem>
            <SelectItem value="LIVE_MERCHANT_EFFECT">{l("Merchant effects", "판매처 효과")}</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <Field id="live-control-reason" label={l("Operational reason", "운영 사유")}>
        <Textarea id="live-control-reason" value={reason} onChange={(event) => setReason(event.target.value)} />
      </Field>
      <Field id="live-control-confirmation" label={l("Type exactly: {phrase}", "정확히 입력: {phrase}", { phrase: liveConfirmation[scope] })}>
        <Input id="live-control-confirmation" value={confirmation} autoComplete="off" onChange={(event) => setConfirmation(event.target.value)} />
      </Field>
      {feedback ? <Notice tone={feedback.danger ? "danger" : "neutral"}>{feedback.text}</Notice> : null}
      <Button emphasis="danger" busy={working} disabled={working || reason.trim().length < 8 || confirmation !== liveConfirmation[scope]} onClick={() => void submitKill()}>{l("Kill selected Live scope", "선택한 Live 범위 종료")}</Button>
    </CardContent>
  </Card>;
}

function StatusOverview({ health }: { health: OpsHealth }) {
  const { l } = useLocale();
  const pool = health.core.databasePool;
  return (
    <div className="product-ui-ops__overview">
      <div className="product-ui-ops__overview-status">
        <OverallBadge status={health.status} />
        <span>
          {l("core", "core")} {health.core.status} · {l("DB connections", "DB 연결")} {pool.inUse}/{pool.maxOpenConnections}
        </span>
      </div>
      {health.degradedReasonCodes.length > 0 && (
        <ul className="product-ui-ops__reasons">
          {health.degradedReasonCodes.map((code) => (
            <li key={code}>
              <Badge variant="outline">{code}</Badge>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function OverallBadge({ status }: { status: OpsHealth["status"] }) {
  const { l } = useLocale();
  if (status === "ready") {
    return <Badge className="product-ui-ops__badge-ok">{l("Operational", "정상 운영")}</Badge>;
  }
  if (status === "degraded") {
    return <Badge className="product-ui-ops__badge-warn">{l("Partially degraded", "부분 저하")}</Badge>;
  }
  return <Badge variant="destructive">{l("Core unavailable", "코어 불능")}</Badge>;
}

function heartbeatBadge(status: string, l: Localize) {
  switch (status) {
    case "up":
      return <Badge className="product-ui-ops__badge-ok">{l("Up", "정상")}</Badge>;
    case "grace":
      return <Badge className="product-ui-ops__badge-warn">{l("Grace", "유예")}</Badge>;
    case "down":
      return <Badge variant="destructive">{l("Down", "중단")}</Badge>;
    case "paused":
      return <Badge variant="ghost">{l("Paused", "일시중지")}</Badge>;
    default:
      return <Badge variant="outline">{status}</Badge>;
  }
}

function HeartbeatsCard({
  heartbeats,
  failed,
}: {
  heartbeats: OpsHeartbeats | null;
  failed: boolean;
}) {
  const { l } = useLocale();
  return (
    <Card>
      <CardHeader>
        <CardTitle>{l("L1 · Healthchecks heartbeats", "L1 · Healthchecks 하트비트")}</CardTitle>
        <CardDescription>
          {l("Signals sent by backup, WAL ship, PITR drill, and host watch. Healthchecks integrations own Telegram and email notifications.", "backup·WAL ship·PITR drill·host watch가 보내는 신호입니다. Telegram·email 통지는 Healthchecks integration이 소유합니다.")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {failed && (
          <p className="product-ui-ops__muted">
            {l("We couldn't load Healthchecks status. Check it directly in the Healthchecks dashboard.", "Healthchecks 상태를 불러오지 못했습니다. Healthchecks 대시보드에서 직접 확인해 주세요.")}
          </p>
        )}
        {!failed && heartbeats && !heartbeats.configured && (
          <p className="product-ui-ops__muted">
            {l("Not connected — set a read-only API key in the server's HEALTHCHECKS_API_KEY environment variable to show check status here. Alert delivery is unaffected.", "연결 안 됨 — 서버 환경변수 HEALTHCHECKS_API_KEY에 read-only API 키를 설정하면 이 카드에 체크 상태가 나타납니다. 경보 발송에는 영향이 없습니다.")}
          </p>
        )}
        {!failed && heartbeats?.configured && (
          <ul className="product-ui-ops__heartbeats">
            {heartbeats.checks.map((check) => (
              <li key={check.slug || check.name}>
                {heartbeatBadge(check.status, l)}
                <strong>{check.name}</strong>
                <span>
                  {check.lastPing
                    ? l("Last signal {time}", "마지막 신호 {time}", { time: formatRelative(check.lastPing) })
                    : l("No signal history", "신호 기록 없음")}
                  {check.schedule
                    ? ` · ${check.schedule}`
                    : check.timeoutSeconds
                      ? l(" · Interval {duration}", " · 주기 {duration}", { duration: formatDuration(check.timeoutSeconds) })
                      : ""}
                </span>
              </li>
            ))}
            {heartbeats.checks.length === 0 && (
              <li className="product-ui-ops__muted">{l("There are no registered checks.", "등록된 체크가 없습니다.")}</li>
            )}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function ReadbackGrid({ health }: { health: OpsHealth }) {
  const { l } = useLocale();
  const observed = health.observed;
  const settlement = health.settlement;
  const errorRate5m = health.http.requestsLast5m > 0
    ? (health.http.serverErrorsLast5m / health.http.requestsLast5m) * 100
    : 0;
  const cursorLag = settlement && settlement.finalizedCursorSeen &&
      settlement.rpcFinalizedBlock > 0
    ? Math.max(0, settlement.rpcFinalizedBlock - settlement.finalizedCursor)
    : null;
  const intelRate = observed
    ? observed.intelligence.failureRate24h * 100
    : null;
  return (
    <div className="product-ui-ops__grid">
      <ReadbackCard
        title={l("HTTP", "HTTP")}
        rows={[
          {
            label: l("5m requests / 5xx", "5분 요청 / 5xx"),
            value: `${health.http.requestsLast5m} / ${health.http.serverErrorsLast5m}`,
            alarm: health.http.serverErrorsLast5m > 0,
          },
          {
            label: l("5m error rate", "5분 오류율"),
            value: `${errorRate5m.toFixed(1)}%`,
          },
          {
            label: l("60m requests / 5xx", "60분 요청 / 5xx"),
            value: `${health.http.requestsLast60m} / ${health.http.serverErrorsLast60m}`,
          },
        ]}
      />
      <ReadbackCard
        title={l("Settlement reconcile", "Settlement reconcile")}
        rows={settlement ? [
          {
            label: l("Status", "상태"),
            value: settlement.status,
            alarm: settlement.status !== "ready",
          },
          {
            label: l("Finalized cursor lag", "finalized cursor 지연"),
            value: cursorLag === null ? l("Waiting for cursor", "커서 대기 중") : `${cursorLag} blocks`,
            alarm: (cursorLag ?? 0) > 900,
          },
          {
            label: l("Outbox conflicts", "outbox 충돌"),
            value: String(settlement.outboxConflictCount),
            alarm: settlement.outboxConflictCount > 0,
          },
        ] : [{ label: l("Status", "상태"), value: l("Settlement runtime disabled", "settlement runtime 꺼짐") }]}
      />
      <ReadbackCard
        title={l("Intelligence (24h)", "Intelligence (24h)")}
        rows={observed ? [
          {
            label: l("Attempts / failures", "시도 / 실패"),
            value: `${observed.intelligence.attempts24h} / ${observed.intelligence.failed24h}`,
          },
          {
            label: l("Failure rate", "실패율"),
            value: `${(intelRate ?? 0).toFixed(1)}%`,
            alarm: (intelRate ?? 0) >= 50,
          },
          {
            label: l("Unresolved EFFECT_UNKNOWN", "EFFECT_UNKNOWN 미해결"),
            value: String(observed.intelligence.effectUnknownOpen),
            alarm: observed.intelligence.effectUnknownOpen > 0,
          },
        ] : []}
      />
      <ReadbackCard
        title={l("PII (24h)", "PII (24h)")}
        rows={observed ? [
          {
            label: l("Access granted / denied", "열람 허용 / 거부"),
            value: `${observed.piiAccess.granted24h} / ${observed.piiAccess.denied24h}`,
          },
          {
            label: l("Broken audit-chain links", "감사 체인 끊김"),
            value: String(observed.piiAccess.brokenChainLinks),
            alarm: observed.piiAccess.brokenChainLinks > 0,
          },
          {
            label: l("Pending deletion / stale keys", "삭제 대기 / 구키 잔여"),
            value: `${observed.piiLifecycle.pendingDeletions} / ${observed.piiLifecycle.staleKeyRows}`,
          },
        ] : []}
      />
      <ReadbackCard
        title={l("Cost reservations", "Cost 예약")}
        rows={observed ? [
          {
            label: l("UNKNOWN count", "UNKNOWN 건수"),
            value: String(observed.costReservations.unknownCount),
            alarm: observed.costReservations.unknownCount > 0,
          },
          {
            label: l("Oldest UNKNOWN", "가장 오래된 UNKNOWN"),
            value: observed.costReservations.unknownCount > 0
              ? formatDuration(observed.costReservations.oldestUnknownAgeSeconds)
              : l("None", "없음"),
          },
        ] : []}
      />
      <ReadbackCard
        title={l("Accounts", "계정")}
        rows={observed ? [
          {
            label: l("Active sessions", "활성 세션"),
            value: String(observed.accounts.activeSessions),
          },
          {
            label: l("New signups in 24h", "24h 신규 가입"),
            value: String(observed.accounts.usersCreated24h),
          },
          {
            label: l("Active users in 24h", "24h 활성 사용자"),
            value: String(observed.accounts.activeUsers24h),
          },
        ] : []}
      />
    </div>
  );
}

function ReadbackCard({
  title,
  rows,
}: {
  title: string;
  rows: Array<{ label: string; value: string; alarm?: boolean }>;
}) {
  const { l } = useLocale();
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="product-ui-ops__muted">{l("We couldn't load this summary.", "집계를 불러오지 못했습니다.")}</p>
        ) : (
          <dl className="product-ui-ops__rows">
            {rows.map((row) => (
              <div key={row.label}>
                <dt>{row.label}</dt>
                <dd className={row.alarm ? "is-alarm" : undefined}>
                  {row.value}
                </dd>
              </div>
            ))}
          </dl>
        )}
      </CardContent>
    </Card>
  );
}

function HostCard({ host }: { host: OpsHealth["host"] }) {
  const { l } = useLocale();
  return (
    <Card>
      <CardHeader>
        <CardTitle>{l("L3 · Host", "L3 · 호스트")}</CardTitle>
        <CardDescription>
          {l("Facts recorded every five minutes by host watch. Watch sends threshold alerts (A-08–A-10, A-18) directly to Healthchecks.", "호스트 watch가 5분마다 남기는 팩트입니다. 임계 초과 경보(A-08~A-10, A-18)는 watch가 Healthchecks로 직접 보냅니다.")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {!host && (
          <p className="product-ui-ops__muted">
            {l("No host facts are available. They appear only on production servers with the ops-watch timer installed.", "호스트 팩트가 없습니다. ops-watch 타이머가 설치된 운영 서버에서만 나타납니다.")}
          </p>
        )}
        {host && (
          <div className="product-ui-ops__host">
            {host.stale && (
              <p className="product-ui-ops__stale">
                {l("The fact is {age} old — check the watch timer status (A-04).", "팩트가 {age} 전 것입니다 — watch 타이머 상태를 확인하세요 (A-04).", { age: formatDuration(host.ageSeconds) })}
              </p>
            )}
            <div className="product-ui-ops__gauges">
              <HostGauge label={l("Disk usage", "디스크 사용률")} percent={host.diskUsedPct} warnAt={80} />
              <HostGauge label={l("Memory usage", "메모리 사용률")} percent={host.memoryUsedPct} warnAt={90} />
              <div className="product-ui-ops__gauge">
                <span>{l("TLS certificate", "TLS 인증서")}</span>
                <strong className={
                  host.tlsDaysLeft !== null && host.tlsDaysLeft < 14
                    ? "is-alarm"
                    : undefined
                }>
                  {host.tlsDaysLeft === null
                    ? l("Unavailable", "확인 불가")
                    : l("{days} days left", "{days}일 남음", { days: host.tlsDaysLeft })}
                </strong>
              </div>
            </div>
            <ul className="product-ui-ops__services">
              {Object.entries(host.services).sort(([a], [b]) =>
                a.localeCompare(b),
              ).map(([name, service]) => (
                <li key={name}>
                  <Badge
                    variant={service.state === "running" ? "secondary" : "destructive"}
                  >
                    {service.state}
                  </Badge>
                  <strong>{name}</strong>
                  <span>{l("{count} restarts", "재시작 {count}회", { count: service.restartCount })}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function HostGauge({
  label,
  percent,
  warnAt,
}: {
  label: string;
  percent: number;
  warnAt: number;
}) {
  return (
    <div className="product-ui-ops__gauge">
      <span>{label}</span>
      <strong className={percent >= warnAt ? "is-alarm" : undefined}>
        {percent}%
      </strong>
    </div>
  );
}

type TrendMetric = {
  key: string;
  title: (l: Localize) => string;
  unit: (l: Localize) => string;
  max?: number;
  pick: (point: OpsSamplePoint) => number | null;
};

const trendMetrics: TrendMetric[] = [
  {
    key: "disk",
    title: (l) => l("Disk usage", "디스크 사용률"),
    unit: (l) => l("%", "%"),
    max: 100,
    pick: (point) => point.diskUsedPct ?? null,
  },
  {
    key: "memory",
    title: (l) => l("Memory usage", "메모리 사용률"),
    unit: (l) => l("%", "%"),
    max: 100,
    pick: (point) => point.memoryUsedPct ?? null,
  },
  {
    key: "errors",
    title: (l) => l("5xx (5m window)", "5xx (5분 창)"),
    unit: (l) => l("", "건"),
    pick: (point) => point.serverErrors5m,
  },
  {
    key: "intel",
    title: (l) => l("Intelligence failure rate (24h)", "Intelligence 실패율(24h)"),
    unit: (l) => l("%", "%"),
    max: 100,
    pick: (point) =>
      point.intelligenceFailureRate24h === undefined
        ? null
        : point.intelligenceFailureRate24h * 100,
  },
  {
    key: "sessions",
    title: (l) => l("Active sessions", "활성 세션"),
    unit: (l) => l("", "개"),
    pick: (point) => point.activeSessions ?? null,
  },
  {
    key: "unknown",
    title: (l) => l("UNKNOWN cost reservations", "UNKNOWN cost 예약"),
    unit: (l) => l("", "건"),
    pick: (point) => point.unknownReservations ?? null,
  },
];

function TrendsCard({
  samples,
  range,
  onRangeChange,
}: {
  samples: OpsSamples | null;
  range: TrendRange;
  onRangeChange: (next: TrendRange) => void;
}) {
  const { l } = useLocale();
  const rangeOptions: Array<{ value: TrendRange; label: string }> = [
    { value: 24, label: l("24 hours", "24시간") },
    { value: 168, label: l("7 days", "7일") },
    { value: 720, label: l("30 days", "30일") },
  ];
  return (
    <Card>
      <CardHeader>
        <CardTitle>{l("Trends", "시계열")}</CardTitle>
        <CardDescription>
          {l("Trends based on five-minute samples retained for 35 days. Alert-catalog thresholds are the decision criteria.", "5분 샘플(35일 보존) 기반 추이입니다. 판단 기준은 경보 카탈로그의 임계값입니다.")}
        </CardDescription>
        <div className="product-ui-ops__range">
          <Selection
            ariaLabel={l("Trend range", "시계열 조회 기간")}
            onChange={(value) => {
              const next = Number(value);
              if (next === 24 || next === 168 || next === 720) {
                onRangeChange(next);
              }
            }}
            options={rangeOptions.map((option) => ({
              ariaLabel: l("View {range}", "{range} 조회", { range: option.label }),
              label: option.label,
              value: String(option.value),
            }))}
            value={String(range)}
          />
        </div>
      </CardHeader>
      <CardContent>
        {!samples || samples.points.length < 2 ? (
          <p className="product-ui-ops__muted">
            {l("There are not enough samples yet. They accumulate every five minutes after deployment.", "샘플이 아직 부족합니다. 배포 후 5분 간격으로 쌓입니다.")}
          </p>
        ) : (
          <div className="product-ui-ops__trends">
            {trendMetrics.map((metric) => (
              <TrendChart
                key={metric.key}
                metric={metric}
                points={samples.points}
              />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function TrendChart({
  metric,
  points,
}: {
  metric: TrendMetric;
  points: OpsSamplePoint[];
}) {
  const { l } = useLocale();
  const title = metric.title(l);
  const width = 320;
  const height = 96;
  const plot = { left: 6, right: 6, top: 10, bottom: 6 };
  const values = points.map((point) => metric.pick(point));
  const known = values.filter((value): value is number => value !== null);
  const latest = known.length > 0 ? known[known.length - 1] : null;
  const max = metric.max ?? Math.max(1, ...known);
  const min = metric.max ? 0 : Math.min(0, ...known);
  const span = Math.max(max - min, 1e-9);
  const step = points.length > 1
    ? (width - plot.left - plot.right) / (points.length - 1)
    : 0;
  const segments: string[] = [];
  let current: string[] = [];
  values.forEach((value, index) => {
    if (value === null) {
      if (current.length > 0) segments.push(current.join(" "));
      current = [];
      return;
    }
    const x = plot.left + step * index;
    const y = plot.top +
      (height - plot.top - plot.bottom) * (1 - (value - min) / span);
    current.push(`${current.length === 0 ? "M" : "L"} ${x.toFixed(1)} ${y.toFixed(1)}`);
  });
  if (current.length > 0) segments.push(current.join(" "));

  return (
    <figure className="product-ui-ops__trend">
      <figcaption>
        <span>{title}</span>
        <strong>
          {latest === null
            ? "—"
            : `${Number.isInteger(latest) ? latest : latest.toFixed(1)}${metric.unit(l)}`}
        </strong>
      </figcaption>
      <svg role="img" aria-label={title} viewBox={`0 0 ${width} ${height}`}>
        {segments.length === 0 ? (
          <text className="product-ui-ops__trend-empty" x={width / 2} y={height / 2}>
            {l("No data", "데이터 없음")}
          </text>
        ) : (
          segments.map((segment) => (
            <path key={segment.slice(0, 24)} d={segment} />
          ))
        )}
      </svg>
    </figure>
  );
}

function SessionsCard({
  currentUserId,
  sessions,
  onChanged,
}: {
  currentUserId?: string;
  sessions: ActiveSessions | null;
  onChanged: () => void;
}) {
  const { l } = useLocale();
  const [revokeTarget, setRevokeTarget] = useState<{
    userId: string;
    email: string;
  } | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const revoke = useCallback(async () => {
    if (!revokeTarget) return;
    setBusy(true);
    setNotice(null);
    try {
      const result = await revokeUserSessions(revokeTarget.userId, reason.trim());
      setNotice(
        l("Revoked {count} sessions for {email}.", "{email}의 세션 {count}개를 해지했습니다.", { count: result.revokedSessions, email: revokeTarget.email }),
      );
      setRevokeTarget(null);
      setReason("");
      onChanged();
    } catch (caught) {
      if (caught instanceof APIError && caught.code === "FRESH_AUTH_REQUIRED") {
        setNotice(null);
      } else if (caught instanceof APIError) {
        setNotice(l("Revocation failed: {reason}", "해지 실패: {reason}", { reason: caught.message }));
      } else {
        setNotice(l("Revocation failed: Check your connection.", "해지 실패: 연결을 확인해 주세요."));
      }
    } finally {
      setBusy(false);
    }
  }, [l, onChanged, reason, revokeTarget]);

  const reasonValid = reason.trim().length >= 8;
  const groups = useMemo(
    () => groupSessions(sessions?.sessions ?? []),
    [sessions],
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle>{l("Active sessions", "활성 세션")}</CardTitle>
        <CardDescription>
          {l("Each row represents one user. Forced revocation immediately ends all of that user's sessions and records the reason in the operator audit log (ADR-0040 §10).", "한 행은 사용자 한 명입니다. 강제 해지는 그 사용자의 모든 세션을 즉시 끊고 사유와 함께 운영자 감사 기록에 남습니다 (ADR-0040 §10).")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {notice && <p className="product-ui-ops__notice">{notice}</p>}
        {!sessions ? (
          <p className="product-ui-ops__muted">{l("We couldn't load the session list.", "세션 목록을 불러오지 못했습니다.")}</p>
        ) : groups.length === 0 ? (
          <FeedbackState
            state="empty"
            title={l("There are no active sessions", "활성 세션이 없습니다")}
            description={l("Expired or revoked sessions do not appear in this list.", "만료되거나 폐기된 세션은 이 목록에 나타나지 않습니다.")}
          />
        ) : (
          <div className="product-ui-ops__session-list">
            {groups.map((group) => (
              <Disclosure
                className="product-ui-ops__session"
                contentClassName="product-ui-ops__session-detail"
                key={group.userId}
                summary={(
                  <span className="product-ui-ops__session-summary">
                    <span>
                      <strong>{group.email}</strong>
                      <small>
                        {group.userId === currentUserId ? l("Current account · ", "현재 계정 · ") : ""}
                        {group.operator ? l("Operator", "운영자") : l("User", "사용자")}
                      </small>
                    </span>
                    <span>
                      <strong>{l("{count} sessions", "{count}개", { count: group.sessions.length })}</strong>
                      <small>{l("Earliest expiry {time}", "가장 이른 만료 {time}", { time: formatRelative(group.sessions[0].expiresAt) })}</small>
                    </span>
                  </span>
                )}
              >
                <ul>
                  {group.sessions.map((session) => (
                    <li key={session.sessionId}>
                      <code>{short(session.sessionId)}</code>
                      <span>{l("Created {time}", "생성 {time}", { time: formatKST(session.createdAt) })}</span>
                      <span>{l("Expires {time}", "만료 {time}", { time: formatKST(session.expiresAt) })}</span>
                    </li>
                  ))}
                </ul>
                <Button
                  emphasis="danger"
                  size="compact"
                  onClick={() => {
                    setRevokeTarget({ userId: group.userId, email: group.email });
                    setNotice(null);
                  }}
                >
                  {l("Revoke all sessions for this user", "이 사용자의 세션 모두 해지")}
                </Button>
              </Disclosure>
            ))}
          </div>
        )}
        {revokeTarget && (
          <div className="product-ui-ops__revoke">
            <p>
              {l("Revoke all sessions for {email}. Enter a reason of at least eight characters.", "{email}의 모든 세션을 해지합니다. 사유를 8자 이상 남겨 주세요.", { email: revokeTarget.email })}
            </p>
            <Textarea
              aria-label={l("Session revocation reason", "세션 해지 사유")}
              placeholder={l("Example: Proactive block after a lost-device report", "예: 분실 기기 신고에 따른 선제 차단")}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
            <div className="product-ui-ops__revoke-actions">
              <Button
                busy={busy}
                disabled={!reasonValid}
                emphasis="danger"
                onClick={() => void revoke()}
              >
                {l("Revoke", "해지 실행")}
              </Button>
              <Button
                emphasis="quiet"
                onClick={() => {
                  setRevokeTarget(null);
                  setReason("");
                }}
              >
                {l("Cancel", "취소")}
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function UnknownReservationCard({
  count,
  oldestAgeSeconds,
  onChanged,
}: {
  count: number;
  oldestAgeSeconds: number;
  onChanged: () => void;
}) {
  const { l } = useLocale();
  const [reservationId, setReservationId] = useState("");
  const [outcome, setOutcome] = useState<UnknownReservationOutcome>("SETTLED");
  const [evidenceReference, setEvidenceReference] = useState("");
  const [reason, setReason] = useState("");
  const [settledAmount, setSettledAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resolution, setResolution] = useState<UnknownReservationResolution | null>(null);

  const amount = settledAmount.trim() === "" ? undefined : Number(settledAmount);
  const amountInvalid = amount !== undefined && (
    !Number.isSafeInteger(amount) || amount < 0
  );
  const valid = reservationId.trim() !== "" &&
    evidenceReference.trim() !== "" &&
    reason.trim().length >= 8 &&
    !amountInvalid;

  const resolve = useCallback(async () => {
    if (!valid) return;
    setBusy(true);
    setError(null);
    setResolution(null);
    try {
      const result = await resolveUnknownReservation({
        reservationId: reservationId.trim(),
        outcome,
        reasonDetail: reason.trim(),
        evidenceReference: evidenceReference.trim(),
        settledAmountMicros: outcome === "SETTLED" ? amount : undefined,
      });
      setResolution(result);
      setReservationId("");
      setEvidenceReference("");
      setReason("");
      setSettledAmount("");
      onChanged();
    } catch (caught) {
      if (caught instanceof APIError && caught.code === "FRESH_AUTH_REQUIRED") {
        setError(null);
      } else {
        setError(messageOf(caught, l("We couldn't decide the cost reservation.", "비용 reservation을 판정하지 못했습니다.")));
      }
    } finally {
      setBusy(false);
    }
  }, [amount, evidenceReference, onChanged, outcome, reason, reservationId, valid]);

  return (
    <Card>
      <CardHeader>
        <div className="product-ui-ops__cost-header">
          <div>
            <CardTitle>{l("UNKNOWN cost decision", "UNKNOWN 비용 판정")}</CardTitle>
            <CardDescription>
              {l("Verify provider evidence separately, then record only the reservation ID and a limited evidence reference. The provider is not called again.", "provider 증거를 별도로 확인한 뒤 reservation ID와 제한된 증거 참조만 기록합니다. provider를 다시 호출하지 않습니다.")}
            </CardDescription>
          </div>
          <Badge variant={count > 0 ? "destructive" : "outline"}>
            {count > 0 ? l("{count} pending · Oldest {age}", "{count}건 · 최장 {age}", { count, age: formatDuration(oldestAgeSeconds) }) : l("None pending", "대기 없음")}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className="product-ui-ops__cost">
        <Notice tone="warning" title={l("Never decide automatically", "자동 판정하지 않습니다")}>
          {l("Do not enter credentials or raw provider responses. The decision is audited and closes exactly once as SETTLED or RELEASED.", "credential이나 provider 응답 원문은 입력하지 마세요. 판정은 감사 기록에 남고 `SETTLED` 또는 `RELEASED`로 한 번만 닫힙니다.")}
        </Notice>
        {resolution && (
          <Notice announce title={l("Decision recorded", "판정이 기록됐습니다")}>
            {l("Closed {reservation} as {status}.", "{reservation}를 {status}로 닫았습니다.", { reservation: short(resolution.reservationId), status: resolution.status })}
          </Notice>
        )}
        <div className="product-ui-ops__cost-form">
          <Field id="unknown-reservation-id" label={l("Reservation ID", "Reservation ID")} required>
            <Input
              autoComplete="off"
              placeholder={l("UUID", "UUID")}
              value={reservationId}
              onChange={(event) => setReservationId(event.target.value)}
            />
          </Field>
          <Field id="unknown-reservation-outcome" label={l("Decision", "판정")} required>
            <Select
              value={outcome}
              onValueChange={(value) => setOutcome(value as UnknownReservationOutcome)}
            >
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="SETTLED">{l("SETTLED · Cost confirmed", "SETTLED · 비용 발생 확인")}</SelectItem>
                <SelectItem value="RELEASED">{l("RELEASED · No cost confirmed", "RELEASED · 비용 미발생 확인")}</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {outcome === "SETTLED" && (
            <Field
              id="unknown-reservation-amount"
              label={l("Confirmed cost · USD micros", "확인된 비용 · USD micros")}
              hint={l("Leave blank to settle the full reserved amount.", "비워 두면 기존 보류 전액을 정산합니다.")}
              error={amountInvalid ? l("Enter an integer micros amount of zero or more.", "0 이상의 정수 micros를 입력해 주세요.") : undefined}
            >
              <Input
                inputMode="numeric"
                min="0"
                step="1"
                type="number"
                value={settledAmount}
                onChange={(event) => setSettledAmount(event.target.value)}
              />
            </Field>
          )}
          <Field
            id="unknown-reservation-evidence"
            label={l("Provider evidence reference", "Provider 증거 참조")}
            required
            hint={l("Enter only an access location such as a runbook ticket or provider dashboard event ID.", "Runbook ticket나 provider dashboard event ID처럼 접근 위치만 적습니다.")}
          >
            <Input
              autoComplete="off"
              value={evidenceReference}
              onChange={(event) => setEvidenceReference(event.target.value)}
            />
          </Field>
          <Field
            className="product-ui-ops__cost-reason"
            id="unknown-reservation-reason"
            label={l("Decision reason", "판정 사유")}
            required
            hint={l("8–500 characters. Leave enough rationale for another operator to review.", "8–500자. 판단 근거를 다른 운영자가 재검토할 수 있게 남깁니다.")}
            error={error ?? undefined}
          >
            <Textarea value={reason} onChange={(event) => setReason(event.target.value)} />
          </Field>
        </div>
        <div className="product-ui-ops__cost-actions">
          <Button busy={busy} disabled={!valid} onClick={() => void resolve()}>
            {outcome === "SETTLED" ? l("Decide that cost occurred", "비용 발생으로 판정") : l("Decide that no cost occurred", "비용 미발생으로 판정")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function groupSessions(sessions: ActiveSessions["sessions"]): SessionGroup[] {
  const groups = new Map<string, SessionGroup>();
  sessions.forEach((session) => {
    const group = groups.get(session.userId) ?? {
      userId: session.userId,
      email: session.email || session.userId,
      operator: session.operator,
      sessions: [],
    };
    group.sessions.push(session);
    groups.set(session.userId, group);
  });
  return [...groups.values()]
    .map((group) => ({
      ...group,
      sessions: [...group.sessions].sort(
        (left, right) => Date.parse(left.expiresAt) - Date.parse(right.expiresAt),
      ),
    }))
    .sort((left, right) => left.email.localeCompare(right.email));
}

function short(value: string): string {
  return value.length > 14 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function messageOf(caught: unknown, fallback: string): string {
  return caught instanceof Error && caught.message ? caught.message : fallback;
}

function statusLabel(status: string | undefined, l: Localize = localizeFixedCopy): string {
  if (status === "ready") return l("Ready", "정상");
  if (status === "degraded") return l("Needs review", "일부 확인 필요");
  if (status === "unavailable") return l("Unavailable", "사용 불가");
  return l("Unknown", "미확인");
}

function formatKST(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return new Intl.DateTimeFormat(readLocalePreference(), {
    timeZone: "Asia/Seoul",
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function formatRelative(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const deltaSeconds = Math.round((date.getTime() - Date.now()) / 1000);
  const magnitude = Math.abs(deltaSeconds);
  const formatter = new Intl.RelativeTimeFormat(readLocalePreference(), { numeric: "auto" });
  if (magnitude < 60) return formatter.format(deltaSeconds, "second");
  if (magnitude < 3600) return formatter.format(Math.round(deltaSeconds / 60), "minute");
  if (magnitude < 86400) return formatter.format(Math.round(deltaSeconds / 3600), "hour");
  return formatter.format(Math.round(deltaSeconds / 86400), "day");
}

function formatDuration(totalSeconds: number): string {
  if (totalSeconds < 60) return localizeFixedCopy("{count}s", "{count}초", { count: totalSeconds });
  if (totalSeconds < 3600) return localizeFixedCopy("{count}m", "{count}분", { count: Math.round(totalSeconds / 60) });
  if (totalSeconds < 86400) return localizeFixedCopy("{count}h", "{count}시간", { count: Math.round(totalSeconds / 3600) });
  return localizeFixedCopy("{count}d", "{count}일", { count: Math.round(totalSeconds / 86400) });
}
