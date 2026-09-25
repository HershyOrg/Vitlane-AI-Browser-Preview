import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import {
  AppHeader,
  CandidateCard,
  DecisionLane,
  FeedbackState,
  Field,
  Notice,
  PageHeader,
  ProductMedia,
  OrderSummary,
} from "./index";

describe("Vitlane shared UI contracts", () => {
  it("Field가 label, hint, error와 required 상태를 control에 연결한다", () => {
    const markup = renderToStaticMarkup(
      <Field
        id="budget"
        label="예산"
        hint="상품 가격 기준"
        error="0보다 큰 금액을 입력하세요."
        required
      >
        <input id="budget-amount" />
      </Field>,
    );

    expect(markup).toContain('for="budget-amount"');
    expect(markup).toContain('aria-required="true"');
    expect(markup).toContain('aria-invalid="true"');
    expect(markup).toContain(
      'aria-describedby="budget-hint budget-error"',
    );
  });

  it("TEST Notice가 색 없이도 환경을 텍스트로 식별한다", () => {
    const markup = renderToStaticMarkup(
      <Notice tone="test">실제 주문이나 실가치 결제가 아닙니다.</Notice>,
    );

    expect(markup).toContain("TEST");
    expect(markup).toContain("vt-notice__icon");
    expect(markup).not.toContain("border-inline-start");
    expect(markup).toContain("실제 주문이나 실가치 결제가 아닙니다.");
  });

  it("PageHeader는 primary label을 DOM attribute로 누출하지 않는다", () => {
    const markup = renderToStaticMarkup(
      <PageHeader
        title="구매 계획 확인"
        primaryAction={{ label: "구매 계획 확정" }}
      />,
    );

    expect(markup).toContain("구매 계획 확정");
    expect(markup).toContain("vt-button--primary");
    expect(markup).not.toContain('label="구매 계획 확정"');
  });

  it("DecisionLane은 현재 사용자의 확인 단계를 노출한다", () => {
    const markup = renderToStaticMarkup(
      <DecisionLane
        prepared={{
          actor: "Agent",
          title: "후보를 정리했습니다",
          description: "조건에 맞는 상품을 비교했습니다.",
        }}
        current={{
          actor: "나",
          title: "구매 조건을 확인합니다",
          description: "옵션과 금액을 선택합니다.",
        }}
        next={{
          actor: "Vitlane",
          title: "결제로 이어갑니다",
          description: "승인된 조건으로만 준비합니다.",
        }}
      />,
    );

    expect(markup).toContain('aria-current="step"');
    expect(markup).toContain("지금 확인");
    expect(markup).toContain("구매 조건을 확인합니다");
  });

  it("CandidateCard는 장바구니 단일 행동과 현재 상태를 명확히 표시한다", () => {
    const defaultMarkup = renderToStaticMarkup(
      <CandidateCard
        media={{ alt: "모니터", state: "missing" }}
        name="업무용 27인치 모니터"
        optionSummary="검정 · 스탠드 포함"
        price="350.00 USD"
        recommendation="USB-C 전원 공급과 높이 조절을 모두 지원합니다."
        sourceBadge={<span>Shopify</span>}
        iconActions={[
          { kind: "pin", label: "PIN", pressed: true },
          { kind: "like", label: "좋아요", pressed: false },
          { kind: "dislike", label: "싫어요", pressed: false },
        ]}
        primaryAction={{ label: "장바구니 담기" }}
      />,
    );
    const inCartMarkup = renderToStaticMarkup(
      <CandidateCard
        media={{ alt: "모니터", state: "missing" }}
        name="업무용 27인치 모니터"
        optionSummary="검정 · 스탠드 포함"
        price="350.00 USD"
        recommendation="USB-C 전원 공급과 높이 조절을 모두 지원합니다."
        state="in-cart"
        primaryAction={{ emphasis: "quiet", label: "다시 장바구니 담기" }}
      />,
    );
    const secondaryMarkup = renderToStaticMarkup(
      <CandidateCard
        media={{ alt: "모니터", state: "missing" }}
        name="다른 모니터"
        optionSummary="옵션 확인 필요"
        price="300.00 USD"
        recommendation="비교 후보입니다."
        primaryAction={{
          emphasis: "secondary",
          label: "장바구니 담기",
        }}
      />,
    );

    expect(defaultMarkup).toContain("vt-candidate-card__primary-action");
    expect(defaultMarkup).toContain("vt-button--primary");
    expect(inCartMarkup).toContain("is-in-cart");
    expect(defaultMarkup).toContain('aria-label="PIN"');
    expect(defaultMarkup).toContain('aria-pressed="true"');
    expect(defaultMarkup).toContain("장바구니 담기");
    expect(defaultMarkup.indexOf("vt-candidate-card__title"))
      .toBeLessThan(defaultMarkup.indexOf("vt-candidate-card__source"));
    expect(defaultMarkup.indexOf("vt-candidate-card__source"))
      .toBeLessThan(defaultMarkup.indexOf("vt-candidate-card__price"));
    expect(defaultMarkup).not.toContain("추천 이유");
    expect(defaultMarkup).not.toContain("구매하기");
    expect(inCartMarkup).toContain("다시 장바구니 담기");
    expect(secondaryMarkup).toContain("vt-button--secondary");
  });

  it("CandidateCard는 저장된 반응을 좌상단 시그널 뱃지로 요약한다", () => {
    const signalsMarkup = renderToStaticMarkup(
      <CandidateCard
        media={{ alt: "모니터", state: "missing" }}
        name="반응이 저장된 모니터"
        price="350.00 USD"
        recommendation="변형별 반응이 카드 뱃지로 모입니다."
        signals={{ pinned: true, liked: true, disliked: true, purchased: true }}
        primaryAction={{ label: "장바구니 담기" }}
      />,
    );
    const plainMarkup = renderToStaticMarkup(
      <CandidateCard
        media={{ alt: "모니터", state: "missing" }}
        name="반응이 없는 모니터"
        price="350.00 USD"
        recommendation="반응이 없으면 뱃지 컨테이너도 없습니다."
        signals={{}}
        primaryAction={{ label: "장바구니 담기" }}
      />,
    );

    expect(signalsMarkup).toContain("vt-candidate-card__signals");
    expect(signalsMarkup).toContain("vt-candidate-card__signal is-pin");
    expect(signalsMarkup).toContain("vt-candidate-card__signal is-like");
    expect(signalsMarkup).toContain("vt-candidate-card__signal is-dislike");
    expect(signalsMarkup).toContain("고정한 옵션 있음");
    expect(signalsMarkup).toContain("좋아요한 옵션 있음");
    expect(signalsMarkup).toContain("별로예요한 옵션 있음");
    expect(signalsMarkup).toContain("vt-candidate-card__signal is-purchased");
    expect(signalsMarkup).toContain("구매함");
    expect(plainMarkup).not.toContain("vt-candidate-card__signals");
  });

  it("ProductMedia는 remote image 보호 속성을 유지한다", () => {
    const markup = renderToStaticMarkup(
      <ProductMedia
        alt="업무용 모니터"
        src="https://images.example.test/monitor.png"
      />,
    );

    expect(markup).toContain('loading="lazy"');
    expect(markup).toContain('referrerPolicy="no-referrer"');
    expect(markup).toContain('fetchPriority="low"');
  });

  it("FeedbackState는 loading과 error를 서로 다른 접근성 상태로 표현한다", () => {
    const loading = renderToStaticMarkup(
      <FeedbackState
        state="loading"
        description="추천 상품을 정리하고 있습니다."
      />,
    );
    const error = renderToStaticMarkup(
      <FeedbackState
        state="error"
        description="상품 정보를 불러오지 못했습니다."
      />,
    );

    expect(loading).toContain('aria-busy="true"');
    expect(error).toContain('role="alert"');
  });

  it("AppHeader와 OrderSummary가 navigation과 승인 조건을 구조화한다", () => {
    const markup = renderToStaticMarkup(
      <>
        <AppHeader
          navigation={[
            { href: "/", label: "구매 홈", current: true },
	        { href: "/agencyOrder", label: "결제·주문" },
          ]}
        />
        <OrderSummary
          title="업무용 모니터"
          rows={[
            { label: "총 결제 금액", value: "350.00 tVITUSD" },
            { label: "환경", value: "GIWA Sepolia · TEST" },
          ]}
        />
      </>,
    );

    expect(markup).toContain('aria-current="page"');
    expect(markup).toContain("구매 조건");
    expect(markup).toContain("GIWA Sepolia · TEST");
  });
});
