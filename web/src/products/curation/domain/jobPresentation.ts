import type { IntelligenceStep } from "./types";
import { localizeFixedCopy, type Localize } from "../../../shared/i18n";

/**
 * User-facing labels for intelligence job steps and failure reasons.
 *
 * Shared by the PLANNING-phase activity panel and the CURATING-phase
 * conversation/target surfaces so every screen describes the same job
 * state with the same words.
 */
export function jobStepLabel(
  kind: IntelligenceStep["kind"],
  l: Localize = localizeFixedCopy,
) {
  return {
    INTERPRETING: l("Interpreting request", "요청 해석 중"),
    SEARCHING_CATALOG: l("Researching product catalogs", "상품 카탈로그 조사 중"),
    RANKING: l("Comparing candidates", "후보 비교 중"),
    SUBMITTING: l("Preparing results", "결과 정리 중"),
  }[kind];
}

export function jobReasonLabel(
  code?: string,
  l: Localize = localizeFixedCopy,
): string {
  return {
    QUOTA_EXCEEDED: l("You have used today's research allowance.", "오늘 사용 가능한 조사량을 모두 사용했습니다."),
    PROVIDER_UNAVAILABLE: l("Research intelligence is unavailable.", "조사 지능을 사용할 수 없습니다."),
    PROVIDER_RESPONSE_INVALID: l("We couldn't interpret the research result.", "조사 결과를 해석하지 못했습니다."),
    DEADLINE_EXCEEDED: l("Research did not finish within the time limit.", "조사가 제한 시간 안에 끝나지 않았습니다."),
    EXTERNAL_EFFECT_UNKNOWN: l("Checking the research result.", "조사 결과를 확인하는 중입니다."),
    CATALOG_NO_RESULTS: l("No products matched the conditions.", "조건에 맞는 상품을 찾지 못했습니다."),
    CANDIDATE_RANKING_EMPTY: l("No product satisfied both the conditions and available information.", "조건과 확인 가능한 정보를 모두 만족하는 상품을 찾지 못했습니다."),
    CATALOG_UNAVAILABLE: l("We couldn't access the product catalog.", "상품 카탈로그를 조회하지 못했습니다."),
    PROPOSAL_REJECTED: l("The research-item configuration did not pass validation.", "조사 항목 구성이 검증을 통과하지 못했습니다."),
    SUBMISSION_REJECTED: l("The research result did not pass validation.", "조사 결과가 검증을 통과하지 못했습니다."),
    RESEARCH_ROUND_CLOSED: l("This research request is closed. Request new research.", "이 조사 요청은 이미 종결되었습니다. 새로 재조사를 요청해 주세요."),
    INTERNAL_FAILURE: l("We couldn't process the research.", "조사를 처리하지 못했습니다."),
    KOREAN_CATALOG_UNAVAILABLE: l("None of the Korean product sources answered. You can try again shortly.", "한국 상품 소스가 모두 응답하지 않았습니다. 잠시 후 다시 시도할 수 있습니다."),
    RESOURCE_WAIT_EXHAUSTED: l("Research resources stayed busy for too long. You can try again shortly.", "조사 자원이 오래 바빠 이번에는 시작하지 못했습니다. 잠시 후 다시 시도할 수 있습니다."),
    SHOPIFY_SEARCH_FAILED: l("The product catalog search failed. You can try again shortly.", "상품 카탈로그 검색이 실패했습니다. 잠시 후 다시 시도할 수 있습니다."),
    CATALOG_API_RATE_LIMITED: l("Another research request was using the same source. You can try again shortly.", "다른 조사가 같은 소스를 사용 중이었습니다. 잠시 후 다시 시도할 수 있습니다."),
    RESEARCH_INPUT_NORMALIZATION_REQUIRED: l("The search phrase could not be prepared for the selected market.", "선택한 조사 국가에 맞는 검색어를 준비하지 못했습니다."),
    "PHASE8_MANAGED_RANKING_INVALID": l("The AI assessment did not pass validation.", "AI 평가 결과가 검증을 통과하지 못했습니다."),
    "PHASE8_MANAGED_RANKING_UNOBSERVED_PRODUCT": l("The AI assessment referred to a product that was not observed.", "AI 평가가 관찰하지 않은 상품을 참조했습니다."),
    "PHASE8_MANAGED_RANKING_DUPLICATE_PRODUCT": l("The AI assessment repeated a product.", "AI 평가가 같은 상품을 중복 평가했습니다."),
    RESEARCH_UNOBSERVED_FACT: l("The AI assessment cited information that was not observed.", "AI 평가가 관찰하지 않은 정보를 근거로 썼습니다."),
    RESEARCH_PRICE_AXIS_REQUIRED: l("The AI assessment used price outside a price criterion.", "AI 평가가 가격 기준이 아닌 곳에 가격을 사용했습니다."),
  }[code ?? ""] ?? l("We couldn't process the research.", "조사를 처리하지 못했습니다.");
}
