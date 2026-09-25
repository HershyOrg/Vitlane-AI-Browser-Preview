import { useEffect, useState } from "react";
import { getAuthenticationCapabilities } from "../products/account/infra/accountApi";
import { useLocale } from "./i18n";

export type MerchantEffectMode = "SANDBOX" | "LIVE";

// 모드의 단일 소스는 서버 capabilities.merchantEffectMode다(ADR-0057 D3 —
// MANUAL_MERCHANT_EFFECT_ENABLED, Step 6 activation 소유). 조회는 앱 수명당
// 한 번이고 실패는 배너 비표시로 남긴다 — LIVE를 SANDBOX로 오인 표시하지
// 않는다(반대도 마찬가지).
let cachedMode: Promise<MerchantEffectMode | undefined> | undefined;

function fetchMerchantEffectMode() {
  cachedMode ??= getAuthenticationCapabilities()
    .then((capabilities) => capabilities.merchantEffectMode)
    .catch(() => undefined);
  return cachedMode;
}

// 테스트 전용 — 앱 수명 캐시를 케이스 간에 비운다.
export function resetMerchantEffectModeCacheForTests() {
  cachedMode = undefined;
}

export function useMerchantEffectMode() {
  const [mode, setMode] = useState<MerchantEffectMode>();
  useEffect(() => {
    let active = true;
    void fetchMerchantEffectMode().then((value) => {
      if (active) setMode(value);
    });
    return () => { active = false; };
  }, []);
  return mode;
}

// ModeBanner는 AppShell 최상단 고정 스트립이다(ADR-0057 §5) — Sandbox는
// 주황(경고), Live는 초록. 모드를 아직 모르면 아무것도 표시하지 않는다.
export function ModeBanner() {
  const mode = useMerchantEffectMode();
  const { l, t } = useLocale();
  if (!mode) return null;
  if (mode === "LIVE") {
    return (
      <aside className="vt-mode-banner is-live" aria-label={t("mode.label")} role="status">
        <strong>{l("LIVE", "LIVE")}</strong>
        <span>{t("mode.live")}</span>
      </aside>
    );
  }
  return (
    <aside className="vt-mode-banner is-sandbox" aria-label={t("mode.label")} role="status">
      <strong>{l("SANDBOX", "SANDBOX")}</strong>
      <span>{t("mode.sandbox")}</span>
    </aside>
  );
}
