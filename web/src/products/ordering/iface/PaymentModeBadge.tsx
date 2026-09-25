import type { AgencyPaymentSelection } from "../infra/agencyOrderApi";
import { useLocale } from "../../../shared/i18n";
import { Chip } from "../../../shared/ui";

// 주문별 결제 모드 배지(ADR-0057 2차 P0) — 진실은 발행 시 고정된
// paymentSelection.economicEffect다. 테스트 결제는 주황, 실결제는 초록으로
// 반드시 인지된다. 배포 전역 스위치가 아니라 이 주문의 기록에서만 파생한다.
export function PaymentModeBadge({
  selection,
  includeRail = false,
}: {
  selection?: AgencyPaymentSelection;
  includeRail?: boolean;
}) {
  const { l } = useLocale();
  if (!selection) return null;
  const real = selection.economicEffect === "REAL_MONEY";
  return (
    <Chip mode={real ? "live" : "test"}>
      {includeRail
        ? real
          ? l(
              "{rail} · {environment} · real charge",
              "{rail} · {environment} · 실제 청구",
              { rail: selection.rail, environment: selection.providerEnvironment },
            )
          : l(
              "{rail} · {environment} · no real charge",
              "{rail} · {environment} · 실제 청구 없음",
              { rail: selection.rail, environment: selection.providerEnvironment },
            )
        : real
        ? l(
            "Live payment · real charge ({environment})",
            "Live 결제 · 실제 청구 ({environment})",
            { environment: selection.providerEnvironment },
          )
        : l(
            "Test payment · no real charge ({environment})",
            "테스트 결제 · 실제 청구 없음 ({environment})",
            { environment: selection.providerEnvironment },
          )}
    </Chip>
  );
}
