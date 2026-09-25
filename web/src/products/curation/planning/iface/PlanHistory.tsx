import { Link } from "react-router";
import type { PlanResult } from "../../../../shared/api/types";
import {
  buttonClassName,
  PageHeader,
  OrderSummary,
} from "../../../../shared/ui";
import { useLocale } from "../../../../shared/i18n";

export function PlanIntentHistory({ result }: { result: PlanResult }) {
  const { l } = useLocale();
  return (
    <section className="shell-page">
      <PageHeader
        eyebrow={l("Request · read only", "요청 내용 · 읽기 전용")}
        title={l("Original purchase request", "처음 저장한 구매 요청")}
        description={l("Review the original conditions without changing current progress.", "현재 진행 상태를 바꾸지 않고 처음 요청한 조건을 확인합니다.")}
      />
      <div className="shell-callout">
        <strong>{l("Requested product and conditions", "원하는 상품과 조건")}</strong>
        <p>{result.plan.originalIntent}</p>
      </div>
      <OrderSummary
        title={l("Request conditions", "요청 조건")}
        rows={[
          {
            label: l("Original budget", "최초 요청 예산"),
            value: `${result.plan.totalBudget.amount} ${result.plan.totalBudget.currency}`,
          },
          {
            label: l("Order environment", "주문 환경"),
            value:
              result.plan.executionMode === "EXPERIMENT" ? "TEST" : "LIVE",
          },
          {
            label: l("Shipping region", "배송 지역"),
            value: [
              result.plan.locationContext.country,
              result.plan.locationContext.city,
            ].filter(Boolean).join(" · "),
          },
        ]}
      />
      <HistoryReturn result={result} />
    </section>
  );
}

function HistoryReturn({ result }: { result: PlanResult }) {
  const { l } = useLocale();
  return (
    <div className="shell-form-actions">
      <p>{l("This record cannot change the current state.", "이 기록에서는 현재 상태를 변경할 수 없습니다.")}</p>
      <Link
        className={buttonClassName({ emphasis: "primary" })}
        to={result.journey.resumePath}
      >
        {l("Return to current stage", "현재 단계로 돌아가기")}
      </Link>
    </div>
  );
}
