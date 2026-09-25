import { CatalogAPIUsage } from "../../../research/iface/CatalogAPIUsage";
import { ResearchRoundSummary } from "../../../research/iface/ResearchRoundSummary";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useCurrentUser } from "../../../../account/app/useCurrentUser";
import { Badge, ButtonLink, Card, CardContent, CardDescription, CardHeader, CardTitle, FeedbackState, PageHeader, Progress, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Selection } from "../../../../../shared/ui";
import {
  getServerAPIUsage,
  type APIUsageDay,
  type ServerAPIUsage,
} from "../infra/adminUsageApi";
import "./operator-api-usage.css";
import { readLocalePreference, useLocale } from "../../../../../shared/i18n";

type UsageRange = 7 | 30 | 90;

export function OperatorAPIUsagePage() {
  const { l } = useLocale();
  const { user } = useCurrentUser();
  const operator = Boolean(user?.marketingAdmin || user?.phase5Operator);
  const [range, setRange] = useState<UsageRange>(30);
  const [usage, setUsage] = useState<ServerAPIUsage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (nextRange: UsageRange, quiet = false) => {
    if (!operator) {
      setLoading(false);
      return;
    }
    if (!quiet) setLoading(true);
    setError(null);
    try {
      setUsage(await getServerAPIUsage(nextRange));
    } catch {
      setError(
        l("We couldn't load server API usage. Check your connection and try again.", "서버 API 사용량을 불러오지 못했습니다. 연결을 확인한 뒤 다시 시도해 주세요."),
      );
    } finally {
      if (!quiet) setLoading(false);
    }
  }, [l, operator]);

  useEffect(() => {
    void load(range);
  }, [load, range]);

  const today = usage?.days.at(-1);
  const tableDays = useMemo(
    () => usage ? [...usage.days].reverse() : [],
    [usage],
  );
  const rangeOptions: Array<{ value: UsageRange; label: string }> = [
    { value: 7, label: l("7 days", "7일") },
    { value: 30, label: l("30 days", "30일") },
    { value: 90, label: l("90 days", "90일") },
  ];

  if (!operator) {
    return (
      <section className="catalog-ui-admin-main">
        <PageHeader
          eyebrow={l("Access", "접근 권한")}
          title={l("You can't view API usage", "API 사용량을 볼 수 없습니다")}
          description={l("This page is available only to authorized operators.", "이 화면은 허용된 운영자에게만 열립니다.")}
        />
        <ButtonLink href="/">{l("Go to shopping home", "구매 홈으로 이동")}</ButtonLink>
      </section>
    );
  }

  return (
    <section className="catalog-ui-admin-main product-ui-api-usage">
      <PageHeader
        eyebrow={l("Operations", "운영")}
        title={l("API usage", "API 사용량")}
        description={l("Manage research APIs and review model usage and costs.", "상품 조사 API를 관리하고 모델 사용량과 비용을 확인합니다.")}
        secondaryActions={[{
          label: l("Refresh", "새로고침"),
          disabled: loading,
          onClick: () => void load(range, true),
        }]}
        summary={(
          <div className="product-ui-api-usage__range">
            <span>{l("Range", "조회 기간")}</span>
            <Selection
              ariaLabel={l("API usage range", "API 사용량 조회 기간")}
              onChange={(value) => {
                const next = Number(value);
                if (next === 7 || next === 30 || next === 90) {
                  setRange(next);
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
        )}
      />

      <CatalogAPIUsage includeAmazon />
      <ResearchRoundSummary />

      {loading && !usage && (
        <FeedbackState
          state="loading"
          description={l("Summarizing the server's daily token and cost ledger.", "서버의 일별 토큰과 비용 원장을 집계하고 있습니다.")}
        />
      )}
      {error && !usage && (
        <FeedbackState
          state="error"
          title={l("We couldn't load API usage", "API 사용량을 불러오지 못했습니다")}
          description={error}
          action={{ label: l("Try again", "다시 시도"), onAction: () => void load(range) }}
        />
      )}

      {usage && today && (
        <>
          {error && (
            <FeedbackState
              state="error"
              title={l("We couldn't refresh to the latest usage", "최신 사용량으로 갱신하지 못했습니다")}
              description={error}
              action={{
                label: l("Try again", "다시 시도"),
                onAction: () => void load(range, true),
              }}
            />
          )}

          <div className="product-ui-api-usage__status">
            <Badge variant={usage.enabled ? "secondary" : "outline"}>
              {usage.enabled ? l("Managed API enabled", "Managed API 활성") : l("Managed API disabled", "Managed API 비활성")}
            </Badge>
            <span>
              {usage.from} — {usage.through} · {usage.timezone}
            </span>
          </div>

          <div className="product-ui-api-usage__metrics">
            <UsageMetric
              label={l("Tokens today", "오늘 토큰")}
              value={formatInteger(today.totalTokens)}
              detail={l("Input {input} · Output {output}", "입력 {input} · 출력 {output}", { input: formatInteger(today.inputTokens), output: formatInteger(today.outputTokens) })}
            />
            <UsageMetric
              label={l("Settled cost today", "오늘 정산 비용")}
              value={formatUSD(today.settledMicros)}
              detail={today.reservedMicros > 0
                ? l("Plus {amount} reserved", "예약 {amount} 별도", { amount: formatUSD(today.reservedMicros) })
                : l("No unsettled reservations", "미정산 예약 없음")}
            />
            <UsageMetric
              label={l("Budget usage today", "오늘 예산 점유")}
              value={formatPercent(today.usagePercent)}
              detail={l("Daily limit {amount}", "일일 한도 {amount}", { amount: formatUSD(usage.dailyLimitMicros) })}
              progress={today.usagePercent}
            />
            <UsageMetric
              label={l("API calls over {days} days", "{days}일 API 호출", { days: range })}
              value={l("{count} calls", "{count}회", { count: formatInteger(usage.totals.requestCount) })}
              detail={l("{count} cumulative tokens", "누적 {count} tokens", { count: formatInteger(usage.totals.totalTokens) })}
            />
          </div>

          <UsageHistoryChart usage={usage} />

          <Card className="product-ui-api-usage__ledger">
            <CardHeader>
              <CardTitle>{l("Daily ledger", "일별 원장")}</CardTitle>
              <CardDescription>
                {l("Cost usage combines settled cost with active reservations and compares them with the daily server limit.", "비용 점유율은 정산 비용과 진행 중 예약을 합쳐 일일 서버 한도와 비교합니다.")}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{l("UTC date", "UTC 일자")}</TableHead>
                    <TableHead>{l("Calls", "호출")}</TableHead>
                    <TableHead>{l("Input tokens", "입력 토큰")}</TableHead>
                    <TableHead>{l("Output tokens", "출력 토큰")}</TableHead>
                    <TableHead>{l("Total tokens", "전체 토큰")}</TableHead>
                    <TableHead>{l("Settled cost", "정산 비용")}</TableHead>
                    <TableHead>{l("Budget usage", "예산 점유")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {tableDays.map((day) => (
                    <TableRow key={day.usageDate}>
                      <TableCell>
                        <time dateTime={day.usageDate}>
                          {formatDate(day.usageDate)}
                        </time>
                      </TableCell>
                      <TableCell>{l("{count} calls", "{count}회", { count: formatInteger(day.requestCount) })}</TableCell>
                      <TableCell>{formatInteger(day.inputTokens)}</TableCell>
                      <TableCell>{formatInteger(day.outputTokens)}</TableCell>
                      <TableCell>{formatInteger(day.totalTokens)}</TableCell>
                      <TableCell>{formatUSD(day.settledMicros)}</TableCell>
                      <TableCell>
                        <span className="product-ui-api-usage__table-percent">
                          {formatPercent(day.usagePercent)}
                          {day.reservedMicros > 0 && <small>{l("Includes reservations", "예약 포함")}</small>}
                        </span>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      )}
    </section>
  );
}

function UsageMetric({
  detail,
  label,
  progress,
  value,
}: {
  detail: string;
  label: string;
  progress?: number;
  value: string;
}) {
  return (
    <Card className="product-ui-api-usage__metric">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle>{value}</CardTitle>
      </CardHeader>
      <CardContent>
        {progress !== undefined && (
          <Progress
            aria-label={`${label} ${formatPercent(progress)}`}
            value={Math.min(progress, 100)}
          />
        )}
        <p>{detail}</p>
      </CardContent>
    </Card>
  );
}

function UsageHistoryChart({ usage }: { usage: ServerAPIUsage }) {
  const { l } = useLocale();
  const width = 960;
  const height = 320;
  const plot = { left: 58, right: 28, top: 28, bottom: 54 };
  const plotWidth = width - plot.left - plot.right;
  const plotHeight = height - plot.top - plot.bottom;
  const maxTokens = Math.max(
    1,
    ...usage.days.map((day) => day.totalTokens),
  );
  const barSlot = plotWidth / Math.max(usage.days.length, 1);
  const barWidth = Math.max(2, Math.min(20, barSlot * 0.56));
  const points = usage.days.map((day, index) => ({
    day,
    x: plot.left + barSlot * index + barSlot / 2,
    y: plot.top + plotHeight * (1 - Math.min(day.usagePercent, 100) / 100),
  }));
  const costPath = points
    .map(({ x, y }, index) => `${index === 0 ? "M" : "L"} ${x} ${y}`)
    .join(" ");
  const admissionPercent = usage.dailyLimitMicros > 0
    ? usage.admissionLimitMicros / usage.dailyLimitMicros * 100
    : 0;
  const admissionY =
    plot.top + plotHeight * (1 - Math.min(admissionPercent, 100) / 100);
  const labelStep = Math.max(1, Math.ceil(usage.days.length / 7));

  return (
    <Card className="product-ui-api-usage__chart-card vt-dark-scope">
      <CardHeader>
        <CardTitle>{l("Daily server usage", "일별 서버 사용량")}</CardTitle>
        <CardDescription>
          {l("Token bars are relative to the maximum in the selected period; the cost line is relative to the daily server limit.", "토큰 막대는 기간 내 최대치 대비, 비용 선은 일일 서버 한도 대비 비율입니다.")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="product-ui-api-usage__legend" aria-hidden="true">
          <span><i className="is-token" />{l("Total tokens", "전체 토큰")}</span>
          <span><i className="is-cost" />{l("Cost usage", "비용 점유율")}</span>
          <span><i className="is-admission" />{l("New-call cutoff", "신규 호출 중단선")}</span>
        </div>
        <figure className="product-ui-api-usage__chart">
          <svg
            aria-labelledby="api-usage-chart-title api-usage-chart-description"
            role="img"
            viewBox={`0 0 ${width} ${height}`}
          >
            <title id="api-usage-chart-title">{l("Daily API token and cost trend", "일별 API 토큰과 비용 추이")}</title>
            <desc id="api-usage-chart-description">
              {l("Bars show total input and output tokens from {from} through {through}; the line shows usage relative to the daily server cost limit.", "{from}부터 {through}까지 입력과 출력 토큰 합계 막대, 일일 서버 비용 한도 대비 점유율 선", { from: usage.from, through: usage.through })}
            </desc>
            {[0, 25, 50, 75, 100].map((percent) => {
              const y = plot.top + plotHeight * (1 - percent / 100);
              return (
                <g className="product-ui-api-usage__grid" key={percent}>
                  <line
                    x1={plot.left}
                    x2={width - plot.right}
                    y1={y}
                    y2={y}
                  />
                  <text x={plot.left - 10} y={y + 4}>
                    {percent}%
                  </text>
                </g>
              );
            })}
            <line
              className="product-ui-api-usage__admission"
              x1={plot.left}
              x2={width - plot.right}
              y1={admissionY}
              y2={admissionY}
            />
            {usage.days.map((day, index) => {
              const barHeight = day.totalTokens === 0
                ? 0
                : Math.max(2, plotHeight * day.totalTokens / maxTokens);
              const x = plot.left + barSlot * index + barSlot / 2;
              return (
                <rect
                  className="product-ui-api-usage__token-bar"
                  height={barHeight}
                  key={day.usageDate}
                  rx={barWidth / 2}
                  width={barWidth}
                  x={x - barWidth / 2}
                  y={plot.top + plotHeight - barHeight}
                >
                  <title>
                    {formatDate(day.usageDate)} · {formatInteger(day.totalTokens)} {l("tokens", "tokens")}
                  </title>
                </rect>
              );
            })}
            <path
              className="product-ui-api-usage__cost-line"
              d={costPath}
            />
            {points.map(({ day, x, y }) => (
              <circle
                className="product-ui-api-usage__cost-point"
                cx={x}
                cy={y}
                key={day.usageDate}
                r={3}
              >
                <title>
                  {formatDate(day.usageDate)} · {formatPercent(day.usagePercent)} · {formatUSD(day.spentMicros)}
                </title>
              </circle>
            ))}
            {usage.days.map((day, index) => {
              if (
                index % labelStep !== 0 &&
                index !== usage.days.length - 1
              ) {
                return null;
              }
              const x = plot.left + barSlot * index + barSlot / 2;
              return (
                <text
                  className="product-ui-api-usage__date-label"
                  key={day.usageDate}
                  textAnchor="middle"
                  x={x}
                  y={height - 18}
                >
                  {shortDate(day.usageDate)}
                </text>
              );
            })}
          </svg>
          <figcaption>
            {l("See exact input and output tokens and settled costs in the daily ledger below.", "정확한 입력·출력 토큰과 정산 비용은 아래 일별 원장에서 확인할 수 있습니다.")}
          </figcaption>
        </figure>
      </CardContent>
    </Card>
  );
}

function formatInteger(value: number) {
  return new Intl.NumberFormat(readLocalePreference()).format(value);
}

function formatUSD(micros: number) {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  }).format(micros / 1_000_000);
}

function formatPercent(value: number) {
  if (value > 0 && value < 0.01) return "<0.01%";
  return `${new Intl.NumberFormat(readLocalePreference(), {
    maximumFractionDigits: 2,
  }).format(value)}%`;
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat(readLocalePreference(), {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  }).format(new Date(`${value}T00:00:00Z`));
}

function shortDate(value: string) {
  return new Intl.DateTimeFormat(readLocalePreference(), {
    month: "numeric",
    day: "numeric",
    timeZone: "UTC",
  }).format(new Date(`${value}T00:00:00Z`));
}

export const operatorUsageFormatters = {
  formatPercent,
  formatUSD,
};
