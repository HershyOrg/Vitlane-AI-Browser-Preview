import { useState } from "react";
import monitorImage from "./assets/monitor.svg";
import {
  AppHeader,
  BrandMark,
  Button,
  ButtonLink,
  CandidateCard,
  Chip,
  DecisionLane,
  Disclosure,
  FeedbackState,
  Field,
  Input,
  NativeSelect,
  NativeSelectOption,
  Notice,
  OrderSummary,
  PageHeader,
  ProductMedia,
  ReededGlass,
  Selection,
  Textarea,
  type FeedbackStateKind,
} from "../shared/ui";
import "./design-lab.css";

type IntentShoppingFixtureState =
  | "collapsed"
  | "expanded"
  | "researching"
  | "return";
type FixtureState =
  | "default"
  | FeedbackStateKind
  | IntentShoppingFixtureState;
type FixtureKind =
  | "account"
  | "cart"
  | "checkout"
  | "marketing"
  | "operator"
  | "plan"
  | "research"
  | "standard";

interface ScreenFamily {
  action?: string;
  description: string;
  id: string;
  kind: FixtureKind;
  route: string;
  states: FixtureState[];
  title: string;
}

const screenFamilies: ScreenFamily[] = [
  {
    id: "marketing",
    route: "vitlane.com /",
    title: "Marketing",
    description: "Web3 자산으로 Web2 상품을 찾고 결제까지 맡기는 범위를 설명합니다.",
    action: "제품에서 구매 요청하기",
    kind: "marketing",
    states: ["default", "loading", "partial", "error", "success"],
  },
  {
    id: "marketing-policy",
    route: "vitlane.com /privacy · /terms",
    title: "Marketing policy",
    description: "개인정보와 이용 조건을 읽고 제품으로 돌아갑니다.",
    kind: "standard",
    states: ["default"],
  },
  {
    id: "login",
    route: "/login",
    title: "로그인",
    description: "Google 계정으로 자신의 구매 작업을 안전하게 이어갑니다.",
    action: "Google로 계속",
    kind: "standard",
    states: ["default", "loading", "error", "success"],
  },
  {
    id: "auth-boundary",
    route: "인증 경계",
    title: "인증 확인",
    description: "보호된 작업을 열기 전 서버 세션을 확인합니다.",
    action: "다시 로그인",
    kind: "standard",
    states: ["loading", "error", "success"],
  },
  {
    id: "home",
    route: "/",
    title: "구매 홈",
    description: "진행 중인 구매 요청과 다음 행동을 확인합니다.",
    action: "새 구매 요청",
    kind: "standard",
    states: ["default", "loading", "empty", "error", "success"],
  },
  {
    id: "plan-create",
    route: "/plans/new",
    title: "새 구매 요청",
    description: "찾고 싶은 상품, 예산과 받을 지역을 입력합니다.",
    action: "구매 계획 만들기",
    kind: "account",
    states: ["default", "loading", "error", "success"],
  },
  {
    id: "research",
    route: "/curations/:curationId",
    title: "큐레이션 워크스페이스",
    description: "대상별 조사, 추천 비교, 선택과 구매 요청을 같은 작업 공간에서 이어갑니다.",
    action: "장바구니 담기",
    kind: "research",
    states: [
      "default",
      "collapsed",
      "expanded",
      "researching",
      "loading",
      "empty",
      "partial",
      "error",
      "expired",
      "reconnect",
      "success",
    ],
  },
	  {
	    id: "checkout",
	    route: "/agencyOrder/:agencyOrderId/payment",
	    title: "AgencyOrder 결제",
	    description: "AgencyOrder snapshot, 지갑 서명과 TEST 결제 사건을 구분합니다.",
	    action: "주문 금액 승인",
    kind: "checkout",
    states: ["default", "loading", "partial", "error", "expired", "success"],
  },
  {
    id: "account",
    route: "/account",
    title: "계정과 연결",
    description: "지갑, 배송 정보, KYC와 Agent 연결 상태를 관리합니다.",
    action: "변경사항 저장",
    kind: "account",
    states: ["default", "loading", "empty", "partial", "error", "expired", "reconnect", "success"],
  },
  {
	    id: "agency-order",
	    route: "/agencyOrder",
	    title: "결제·주문",
	    description: "AgencyOrder의 결제, Shop별 처리와 TEST 영수증 상태를 추적합니다.",
    kind: "checkout",
    states: ["default", "loading", "empty", "partial", "error", "success"],
  },
  {
	    id: "agency-order-operator",
	    route: "/admin/agencyOrder",
	    title: "AgencyOrder 처리",
    description: "담당, 제한 주소 열람과 simulated merchant 결과를 감사 가능하게 처리합니다.",
    action: "담당하기",
    kind: "operator",
    states: ["default", "loading", "empty", "partial", "error", "expired", "success"],
  },
  {
    id: "not-found",
    route: "404 · resource error",
    title: "페이지를 찾을 수 없음",
    description: "사용자가 복구 가능한 다음 목적지를 안내합니다.",
    action: "구매 홈으로",
    kind: "standard",
    states: ["error"],
  },
];

const feedbackCopy: Record<
  FeedbackStateKind,
  { action?: string; description: string; title: string }
> = {
  loading: {
    title: "현재 상태를 확인하고 있습니다",
    description: "서버에 저장된 구매 작업을 불러오는 동안 잠시 기다려 주세요.",
  },
  empty: {
    title: "아직 표시할 항목이 없습니다",
    description: "첫 구매 요청을 만들면 여기에 다음 행동이 표시됩니다.",
    action: "새 구매 요청",
  },
  partial: {
    title: "확인된 정보부터 표시합니다",
    description: "일부 가격이나 옵션은 아직 확인 중입니다. 확인되지 않은 값은 결제 조건으로 사용하지 않습니다.",
    action: "확인된 내용 보기",
  },
  error: {
    title: "현재 정보를 불러오지 못했습니다",
    description: "입력한 내용은 유지했습니다. 연결을 확인한 뒤 다시 시도하세요.",
    action: "다시 시도",
  },
  expired: {
    title: "이 조건은 다시 확인해야 합니다",
    description: "가격이나 권한의 유효 시간이 지났습니다. 최신 조건을 확인해 다시 승인하세요.",
    action: "최신 조건 확인",
  },
  reconnect: {
    title: "Agent 연결을 다시 확인해 주세요",
    description: "기존 조사 결과는 보존됩니다. 연결을 복구한 뒤 같은 작업을 다시 맡길 수 있습니다.",
    action: "Agent 다시 연결",
  },
  success: {
    title: "요청한 단계가 완료됐습니다",
    description: "서버에 반영된 결과와 다음에 할 일을 확인할 수 있습니다.",
    action: "다음 단계 보기",
  },
};

const laneSegments = {
  prepared: {
    actor: "Agent",
    title: "구매 조건에 맞는 상품을 정리했습니다",
    description: "예산, 배송 지역과 제외 조건을 기준으로 후보를 비교했습니다.",
  },
  current: {
    actor: "나",
    title: "상품 근거와 옵션을 확인합니다",
    description: "선택 전에는 결제나 주문이 진행되지 않습니다.",
  },
  next: {
    actor: "Vitlane",
    title: "승인한 조건만 결제로 이어갑니다",
    description: "금액 snapshot과 지갑 서명을 별도 단계에서 확인합니다.",
  },
};

const orderRows = [
  {
    label: "상품",
    value: "27인치 USB-C 업무용 모니터",
    detail: "Black · 높이 조절 스탠드",
  },
  {
    label: "상품 가격",
    value: "350.00 USD",
    detail: "shipping·tax NOT_QUOTED",
  },
  {
    label: "결제 예정",
    value: "350.000000 tVITUSD",
    detail: "GIWA Sepolia · 무가치 TEST token",
  },
  {
    label: "지갑",
    value: "0x71A4…09F2",
    detail: "사용자가 직접 승인·서명",
    monospaced: true,
  },
];

export function DesignLabPage() {
  const params = new URLSearchParams(window.location.search);
  const view = params.get("view");

  if (view === "fixture") {
    const requestedFamily = screenFamilies.find(
      ({ id }) => id === params.get("screen"),
    );
    const family = requestedFamily ?? screenFamilies[8];
    const requestedState = params.get("state") as FixtureState | null;
    const state =
      requestedState && family.states.includes(requestedState)
        ? requestedState
        : family.states[0];
    return <ScreenFixture family={family} state={state} />;
  }

  return <DesignLabOverview />;
}

function DesignLabOverview() {
  const [candidateSignal, setCandidateSignal] = useState("monitor");

  return (
    <>
      <a className="lab-skip-link" href="#lab-main">
        Design Lab 본문으로 이동
      </a>
      <div className="lab-shell">
        <aside className="lab-index" data-component-fixture="brand-mark">
          <BrandMark />
          <p className="lab-index__phase">DESIGN PHASE 5</p>
          <nav aria-label="Design Lab 목차">
            <a href="#foundation">Foundation</a>
            <a href="#controls">Controls</a>
            <a href="#feedback">Feedback</a>
            <a href="#handoff">Handoff</a>
            <a href="#commerce">Commerce</a>
            <a href="#screens">Screen coverage</a>
          </nav>
          <p className="lab-index__note">
            실제 UI component와 개인정보 없는 fixture만 사용합니다.
          </p>
        </aside>

        <main className="lab-main" id="lab-main">
          <div data-component-fixture="page-header">
            <PageHeader
              eyebrow="Vitlane interface calibration"
              title="구매 파이프라인을 한 언어로 맞춥니다."
              description="Marketing, 제품 App과 operator가 같은 토큰과 컴포넌트 계약을 사용하는지 상태별로 확인합니다. 이 화면은 운영 UI의 복사본이 아닙니다."
              primaryAction={{ label: "화면 fixture 보기" }}
              secondaryActions={[{ label: "컴포넌트 계약 읽기" }]}
              summary={
                <div className="lab-calibration-strip" aria-label="Vitlane 구매 단계">
                  <span>요청</span>
                  <span>계획</span>
                  <span>조사</span>
                  <span>구매</span>
                </div>
              }
            />
          </div>

          <LabSection
            id="foundation"
            eyebrow="Foundation"
            title="색보다 구조가 먼저 읽혀야 합니다."
            description="Still은 현재 행동 하나에만 쓰고, TEST와 위험은 색과 문구를 함께 사용합니다. Moss·Maple·Iris는 같은 밝기·채도에서 hue만 돌린 팔레트입니다(ADR-0082)."
          >
            <div className="lab-foundation-grid">
              <div className="lab-type-specimen">
                <p className="lab-type-display">첫 출근이어도, 더 잘 삽니다.</p>
                <p className="lab-type-body">
                  사용자가 원하는 상품을 Agent가 찾고, 승인한 구매 조건만 Vitlane이
                  다음 단계로 이어갑니다.
                </p>
                <code>350.000000 tVITUSD · 0x71A4…09F2</code>
              </div>
              <div className="lab-swatches" aria-label="Lane Signal 색상">
                <Swatch name="Night" className="is-night" usage="text" />
                <Swatch name="Cloud" className="is-cloud" usage="canvas" />
                <Swatch name="Clear" className="is-clear" usage="surface" />
                <Swatch name="Alloy" className="is-alloy" usage="rule" />
                <Swatch name="Still" className="is-route" usage="current action · Water" />
                <Swatch name="Moss" className="is-moss" usage="palette · same L·C, hue 138°" />
                <Swatch name="Maple" className="is-maple" usage="palette · hue 40°" />
                <Swatch name="Iris" className="is-iris" usage="palette · hue 308°" />
                <Swatch name="Test Amber" className="is-test" usage="TEST boundary" />
              </div>
            </div>
            <div className="lab-glass-row" data-component-fixture="reeded-glass">
              <figure className="lab-glass">
                <div className="lab-glass__frame">
                  <ReededGlass />
                </div>
                <figcaption>ReededGlass · still</figcaption>
              </figure>
              <figure className="lab-glass">
                <div className="lab-glass__frame">
                  <ReededGlass motion="swell" />
                </div>
                <figcaption>ReededGlass · swell (reduced motion에서는 정지)</figcaption>
              </figure>
              <figure className="lab-glass">
                <div className="lab-glass__frame vt-dark-scope">
                  <ReededGlass />
                </div>
                <figcaption>ReededGlass · dark scope</figcaption>
              </figure>
            </div>
          </LabSection>

          <LabSection
            id="controls"
            eyebrow="Controls"
            title="강조는 행동의 우선순위를 말합니다."
            description="기본 Button은 secondary이며, primary는 현재 완료해야 하는 사건 하나에만 둡니다."
          >
            <div
              className="lab-control-row"
              data-component-fixture="button"
              aria-label="Button 상태"
            >
              <Button emphasis="primary">장바구니 담기</Button>
              <Button>장바구니에서 제거</Button>
              <Button emphasis="quiet">나중에 하기</Button>
              <Button emphasis="danger">구매 요청 폐기</Button>
              <Button busy>저장 중…</Button>
              <Button disabled>현재 사용할 수 없음</Button>
              <span data-component-fixture="button-link">
                <ButtonLink href="#screens" emphasis="quiet">
                  화면 fixture 보기
                </ButtonLink>
              </span>
            </div>
            <div className="lab-field-grid" data-component-fixture="field">
              <Field
                id="lab-budget"
                label="전체 예산"
                hint="상품 가격 기준이며 배송비와 세금은 결제 전에 다시 확인합니다."
                required
              >
                <Input defaultValue="500 USD" />
              </Field>
              <Field
                id="lab-country"
                label="받을 국가"
                hint="현재 조사 범위에 사용합니다."
              >
                <NativeSelect defaultValue="KR">
                  <NativeSelectOption value="KR">대한민국</NativeSelectOption>
                  <NativeSelectOption value="US">미국</NativeSelectOption>
                </NativeSelect>
              </Field>
              <Field
                id="lab-intent"
                label="찾고 싶은 상품"
                error="상품과 필요한 조건을 한 가지 이상 입력하세요."
                required
              >
                <Textarea defaultValue="" />
              </Field>
            </div>
          </LabSection>

          <LabSection
            id="feedback"
            eyebrow="Feedback"
            title="상태는 다음 행동까지 설명합니다."
            description="성공을 초록색으로 칠하는 대신 완료된 일과 복구 방법을 같은 문법으로 보여 줍니다."
          >
            <div className="lab-notice-stack" data-component-fixture="notice">
              <Notice>사용자가 승인하기 전에는 결제나 주문이 진행되지 않습니다.</Notice>
              <Notice tone="test">
                tVITUSD는 경제적 가치가 없는 TEST token이며 실제 상품 주문이
                생성되지 않습니다.
              </Notice>
              <Notice tone="warning" title="가격 일부 확인 중">
                shipping과 tax는 아직 견적되지 않았습니다.
              </Notice>
              <Notice tone="danger" title="구매 조건을 불러오지 못했습니다">
                입력은 유지했습니다. 연결을 확인한 뒤 다시 시도하세요.
              </Notice>
            </div>
            <div
              className="lab-feedback-grid"
              data-component-fixture="feedback-state"
            >
              {(Object.keys(feedbackCopy) as FeedbackStateKind[]).map((state) => (
                <FeedbackState
                  key={state}
                  state={state}
                  title={feedbackCopy[state].title}
                  description={feedbackCopy[state].description}
                  action={
                    feedbackCopy[state].action
                      ? { label: feedbackCopy[state].action }
                      : undefined
                  }
                />
              ))}
            </div>
          </LabSection>

          <LabSection
            id="chips"
            eyebrow="Status"
            title="상태는 점 하나에만 색을 둡니다."
            description="Chip은 문장체 caption이고 색은 점(진행 still·완료 text·대기 빈 원·실패 danger)에만 있습니다. TEST/LIVE 경계만 soft tone 배지로 남고, Selection은 선택 surface + accent 글자이며 ring이 없습니다."
          >
            <div className="lab-chip-row" data-component-fixture="chip">
              <Chip>담당 가능</Chip>
              <Chip tone="progress">판매처 주문 · 주문 중</Chip>
              <Chip tone="done">판매처 주문 · 주문 완료</Chip>
              <Chip tone="waiting">자금 · 생성 전</Chip>
              <Chip tone="failed">판매처 주문 · 실패</Chip>
              <Chip mode="test">테스트 결제 · 실제 청구 없음 (TESTNET)</Chip>
              <Chip mode="live">Live 결제 · 실제 청구 (LIVE)</Chip>
              <Chip attention>확인 필요</Chip>
            </div>
            <div className="lab-chip-row" data-component-fixture="selection">
              <LabSelection />
            </div>
          </LabSection>

          <LabSection
            id="handoff"
            eyebrow="Handoff"
            title="누가 준비했고, 내가 무엇을 확인하는지."
            description="Decision Lane은 자동 progress가 아니라 승인 직전의 책임과 다음 사건을 설명합니다."
          >
            <div data-component-fixture="decision-lane">
              <DecisionLane {...laneSegments} />
            </div>
            <div
              className="lab-header-preview"
              data-component-fixture="app-header"
            >
              <AppHeader
                compact
                navigation={[
                  { href: "#home", label: "큐레이션 홈", current: true },
                  { href: "#new", label: "새 큐레이션" },
                  { href: "#orders", label: "결제·주문" },
                  { href: "#account", label: "계정" },
                ]}
                actions={<Button size="compact">새 큐레이션</Button>}
              />
            </div>
          </LabSection>

          <LabSection
            id="commerce"
            eyebrow="Commerce"
            title="추천 상품은 같은 기준선에서 비교합니다."
            description="기본 카드에는 이미지, 상품명, 가격, 추천 근거, 옵션, 반응과 구매 행동을 둡니다."
          >
            <div
              className="lab-candidate-grid"
              data-component-fixture="candidate-card"
            >
              <CandidateCard
                media={{ alt: "27인치 업무용 모니터", src: monitorImage }}
                merchant="Example Store US"
                name="27인치 USB-C 90W 업무용 모니터"
                price="350.00 USD"
                recommendation="노트북 충전, 높이 조절과 KVM을 모두 지원해 업무용 조건에 가장 잘 맞습니다."
                optionSummary="Black · 높이 조절 스탠드"
                state={candidateSignal === "monitor" ? "pinned" : "default"}
                iconActions={[
                  {
                    kind: "pin",
                    label:
                      candidateSignal === "monitor" ? "PIN 해제" : "PIN",
                    pressed: candidateSignal === "monitor",
                    onAction: () =>
                      setCandidateSignal(
                        candidateSignal === "monitor" ? "" : "monitor",
                      ),
                  },
                  { kind: "like", label: "좋아요", pressed: false },
                  { kind: "dislike", label: "싫어요", pressed: false },
                ]}
                primaryAction={{ label: "장바구니 담기" }}
                details={
                  <p>
                    판매처 관찰 시각, 배송 가능 지역과 상세 비교 근거는 선택 전에
                    여기에서 확인합니다. 기술 hash는 지원용 상세로 분리합니다.
                  </p>
                }
              />
              <CandidateCard
                media={{ alt: "울트라와이드 모니터", state: "missing" }}
                merchant="Global Catalog"
                name="아주 긴 상품명도 두 줄 이후에는 생략되어 카드의 가격과 선택 버튼 기준선이 흔들리지 않는 34인치 울트라와이드 모니터"
                price="1,249,999.00 USD"
                recommendation="넓은 작업 공간은 좋지만 예산을 넘고 배송비가 아직 확인되지 않았습니다. Long English product title and evidence remain inside disclosure."
                optionSummary="색상 선택 필요"
                state={
                  candidateSignal === "wide"
                    ? "configuring"
                    : "configuration-required"
                }
                primaryAction={{
                  label:
                    candidateSignal === "wide"
                      ? "장바구니 담기"
                      : "옵션 확인 후 담기",
                  onAction: () => setCandidateSignal("wide"),
                }}
                details={<p>옵션을 확인한 뒤 장바구니에 담을 수 있습니다.</p>}
              />
              <CandidateCard
                media={{ alt: "휴대용 모니터", state: "error" }}
                merchant="Example Market"
                name="16인치 휴대용 USB-C 모니터"
                price="189.00 USD"
                recommendation="가볍지만 고정 설치와 색 정확도 조건에서는 다른 후보보다 우선순위가 낮습니다."
                optionSummary="Gray · 보호 커버 포함"
                state="pinned"
                iconActions={[
                  { kind: "pin", label: "PIN 해제", pressed: true },
                  { kind: "like", label: "좋아요", pressed: false },
                  { kind: "dislike", label: "싫어요", pressed: false },
                ]}
                primaryAction={{ label: "장바구니 담기" }}
                details={<p>이미지가 없어도 상품명, 가격과 구매 행동은 계속 사용할 수 있습니다.</p>}
              />
            </div>
            <Disclosure
              className="lab-component-states"
              summary="CandidateCard edge states 보기"
            >
              <div className="lab-candidate-grid">
                <CandidateCard
                  media={{ alt: "기본 후보 모니터", state: "missing" }}
                  merchant="Example Store"
                  name="기본 상태의 추천 상품"
                  price="299.00 USD"
                  recommendation="아직 반응을 남기지 않은 기본 비교 대상입니다."
                  optionSummary="옵션 확인 완료"
                  iconActions={[
                    { kind: "pin", label: "PIN", pressed: false },
                    { kind: "like", label: "좋아요", pressed: false },
                    { kind: "dislike", label: "싫어요", pressed: false },
                  ]}
                  primaryAction={{ label: "장바구니 담기" }}
                />
                <CandidateCard
                  media={{ alt: "싫어요 반응 후보 모니터", state: "missing" }}
                  merchant="Example Store"
                  name="사용자가 싫어요를 남긴 추천 상품"
                  price="429.00 USD"
                  recommendation="예산을 넘고 필요한 연결 단자가 없어 선호도가 낮습니다."
                  optionSummary="옵션 확인 완료"
                  iconActions={[
                    { kind: "pin", label: "PIN", pressed: false },
                    { kind: "like", label: "좋아요", pressed: false },
                    { kind: "dislike", label: "싫어요 취소", pressed: true },
                  ]}
                  primaryAction={{ label: "장바구니 담기" }}
                />
                <CandidateCard
                  media={{ alt: "정리 중인 추천 상품", state: "loading" }}
                  merchant="판매처 확인 중"
                  name="추천 상품을 정리하고 있습니다"
                  price="가격 확인 중"
                  recommendation="확인된 추천 근거를 준비하고 있습니다."
                  optionSummary="옵션 확인 중"
                  state="loading"
                  primaryAction={{ label: "장바구니 담기" }}
                />
                <CandidateCard
                  media={{ alt: "반응 뱃지가 있는 추천 상품", state: "missing" }}
                  merchant="Example Store"
                  name="저장한 반응이 카드 모서리에 남는 추천 상품"
                  price="219.00 USD"
                  recommendation="고정과 좋아요, 별로예요 반응이 카드 좌상단 뱃지로 표시됩니다."
                  optionSummary="옵션 확인 완료"
                  signals={{ pinned: true, liked: true, disliked: true }}
                  primaryAction={{ label: "장바구니 담기" }}
                />
              </div>
            </Disclosure>
            <div
              className="lab-candidate-grid"
              data-component-fixture="product-media"
            >
              <ProductMedia
                alt="정상 상품"
                src={monitorImage}
                caption="loaded"
              />
              <ProductMedia alt="이미지 없는 상품" state="missing" />
              <ProductMedia alt="이미지 오류 상품" state="error" />
              <ProductMedia alt="이미지 확인 중인 상품" state="loading" />
            </div>
            <div className="lab-summary-layout">
              <div data-component-fixture="order-summary">
                <OrderSummary
                  headingLevel="h3"
                  title="27인치 USB-C 업무용 모니터"
                  rows={orderRows}
                  boundary={
                    <Notice tone="test">
                      사용자의 승인, ERC-20 allowance와 onchain pay는 서로 다른
                      사건입니다.
                    </Notice>
                  }
                />
              </div>
              <DecisionLane
                prepared={{
                  actor: "Vitlane",
                  title: "승인할 금액을 고정했습니다",
                  description: "상품과 quote snapshot은 승인 뒤 바뀌지 않습니다.",
                }}
                current={{
                  actor: "나",
                  title: "구매 조건을 승인합니다",
                  description: "이 승인은 지갑의 token 이동 서명이 아닙니다.",
                }}
                next={{
                  actor: "Wallet",
                  title: "Allowance와 pay를 각각 서명합니다",
                  description: "현재 가능한 서명 하나만 primary로 표시합니다.",
                }}
              />
            </div>
          </LabSection>

          <LabSection
            id="screens"
            eyebrow="Screen coverage"
            title="현재 route의 핵심 상태를 API 없이 재현합니다."
            description="각 링크는 production component로 조합한 독립 fixture를 열며, 실제 지갑·PII·외부 side effect를 사용하지 않습니다."
          >
            <ScreenCoverageTable />
          </LabSection>
        </main>
      </div>
    </>
  );
}

function LabSelection() {
  const [range, setRange] = useState("30");
  return (
    <Selection
      ariaLabel="조회 기간"
      onChange={setRange}
      options={[
        { value: "7", label: "7일" },
        { value: "30", label: "30일" },
        { value: "90", label: "90일" },
        { value: "365", label: "1년", disabled: true },
      ]}
      value={range}
    />
  );
}

function LabSection({
  children,
  description,
  eyebrow,
  id,
  title,
}: {
  children: React.ReactNode;
  description: string;
  eyebrow: string;
  id: string;
  title: string;
}) {
  return (
    <section className="lab-section" id={id}>
      <header className="lab-section__header">
        <p>{eyebrow}</p>
        <h2>{title}</h2>
        <span>{description}</span>
      </header>
      <div className="lab-section__content">{children}</div>
    </section>
  );
}

function Swatch({
  className,
  name,
  usage,
}: {
  className: string;
  name: string;
  usage: string;
}) {
  return (
    <div className={`lab-swatch ${className}`}>
      <span aria-hidden="true" />
      <strong>{name}</strong>
      <small>{usage}</small>
    </div>
  );
}

function ScreenCoverageTable() {
  return (
    <div className="lab-table-wrap">
      <table className="lab-coverage">
        <thead>
          <tr>
            <th scope="col">화면</th>
            <th scope="col">사용자 목적</th>
            <th scope="col">재현 상태</th>
          </tr>
        </thead>
        <tbody>
          {screenFamilies.map((family) => (
            <tr key={family.id}>
              <th scope="row">
                <strong>{family.title}</strong>
                <code>{family.route}</code>
              </th>
              <td>{family.description}</td>
              <td>
                <div className="lab-state-links">
                  {family.states.map((state) => (
                    <a
                      key={state}
                      href={`?view=fixture&screen=${family.id}&state=${state}`}
                    >
                      {state}
                    </a>
                  ))}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ScreenFixture({
  family,
  state,
}: {
  family: ScreenFamily;
  state: FixtureState;
}) {
  const feedbackState: FeedbackStateKind | null =
    state !== "default" && state in feedbackCopy
      ? (state as FeedbackStateKind)
      : null;
  const stateDetails = feedbackState ? feedbackCopy[feedbackState] : null;
  const showPartialContent = state === "partial";
  const showScenarioContent =
    state === "collapsed" ||
    state === "expanded" ||
    state === "researching" ||
    state === "return";

  return (
    <div className="lab-fixture-page" data-fixture={`${family.id}:${state}`}>
      <AppHeader
        compact
        navigation={[
          { href: "/", label: "구매 홈", current: family.id === "home" },
          { href: "/plans/new", label: "새 구매 요청", current: family.id === "plan-create" },
	          { href: "/agencyOrder", label: "결제·주문", current: family.id === "agency-order" },
          { href: "/account", label: "계정", current: family.id === "account" },
        ]}
        actions={<Button size="compact">새 구매 요청</Button>}
      />
      <main>
        <a className="lab-fixture-back" href="design-lab.html#screens">
          ← Design Lab
        </a>
        <PageHeader
          eyebrow={`${family.route} · ${state}`}
          title={family.title}
          description={family.description}
          primaryAction={
            state === "default" &&
            family.action &&
            family.kind !== "research" &&
            family.kind !== "cart"
              ? { label: family.action }
              : undefined
          }
        />
        {feedbackState && stateDetails && (
          <FeedbackState
            state={feedbackState}
            title={stateDetails.title}
            description={stateDetails.description}
            action={
              stateDetails.action
                ? { label: stateDetails.action }
                : family.action
                  ? { label: family.action }
                  : undefined
            }
          />
        )}
        {(state === "default" || showPartialContent || showScenarioContent) && (
          <FixtureDefaultBody family={family} state={state} />
        )}
      </main>
    </div>
  );
}

function FixtureDefaultBody({
  family,
  state,
}: {
  family: ScreenFamily;
  state: FixtureState;
}) {
  switch (family.kind) {
    case "marketing":
      return (
        <section className="lab-fixture-thesis">
          <p>Web3 자산으로</p>
          <strong>Web2 상품을,</strong>
          <h2>첫 출근이어도, 더 잘 삽니다.</h2>
          <Notice tone="test">
            현재 공개 범위는 실제 주문이 없는 TEST 구매 pipeline입니다.
          </Notice>
        </section>
      );
    case "plan":
      return (
        <section className="lab-fixture-body">
          <DecisionLane {...laneSegments} />
        </section>
      );
    case "research":
      return <CurationFixture state={state} />;
    case "cart":
      return <CartFixture state={state} />;
    case "checkout":
      return (
        <section className="lab-fixture-body lab-fixture-summary">
          <OrderSummary
            title="27인치 USB-C 업무용 모니터"
            rows={orderRows}
            boundary={
              <Notice tone="test">
                실제 merchant order가 생성되지 않는 simulated external effect입니다.
              </Notice>
            }
          />
        </section>
      );
    case "account":
      return (
        <section className="lab-fixture-body lab-fixture-form">
          <Field
            id={`fixture-${family.id}-name`}
            label="표시 이름"
            hint="구매 작업과 알림에 사용합니다."
          >
            <Input defaultValue="민지" />
          </Field>
          <Field
            id={`fixture-${family.id}-region`}
            label="기본 배송 국가"
          >
            <NativeSelect defaultValue="KR">
              <NativeSelectOption value="KR">대한민국</NativeSelectOption>
              <NativeSelectOption value="US">미국</NativeSelectOption>
            </NativeSelect>
          </Field>
        </section>
      );
    case "operator":
      return (
        <section className="lab-fixture-body">
          <Notice tone="test" title="SIMULATED · MANUAL ONLY">
            제한 정보는 담당자와 열람 사유가 확인된 뒤에만 잠시 표시됩니다.
          </Notice>
          <OrderSummary
            headingLevel="h3"
            title="TEST 주문 처리 대상"
            rows={[
              { label: "결제", value: "FINALIZED" },
              { label: "외부 효과", value: "SIMULATED" },
              { label: "Merchant order", value: "생성되지 않음" },
            ]}
          />
        </section>
      );
    default:
      return (
        <section className="lab-fixture-body">
          <Notice>
            이 fixture는 사용자 목적, 현재 상태와 다음 행동을 같은 문장 구조로
            검증합니다.
          </Notice>
        </section>
      );
  }
}

function CurationFixture({ state }: { state: FixtureState }) {
  const showCandidates = state === "default";
  const showResearching = state === "researching";
  const showExpandedComposer = state === "expanded";

  return (
    <section
      className="lab-fixture-body lab-curation-fixture"
      data-scenario-state={state}
      aria-label="큐레이션 상태 fixture"
    >
      <header className="lab-curation-fixture__header">
        <div>
          <p>큐레이션 경로</p>
          <h2>조사 항목 1개</h2>
        </div>
        <span>각 상품군 안에서 조사와 Candidate를 이어갑니다.</span>
      </header>

      <div className="lab-curation-fixture__rail">
        <article className="lab-curation-target">
          <header>
            <div>
              <p>업무용 모니터</p>
              <h2>27인치 USB-C 모니터</h2>
              <span>노트북 충전과 높이 조절을 지원하는 제품</span>
            </div>
            <div className="lab-curation-target__actions">
              <strong>{showResearching ? "조사 중" : "후보 확인"}</strong>
              <Button
                emphasis="danger"
                size="compact"
                aria-label="27인치 USB-C 모니터 해당 상품군 제거"
              >
                해당 상품군 제거
              </Button>
            </div>
          </header>

          {showResearching ? (
            <FeedbackState
              state="loading"
              title="Agent가 조사 중"
              description="현재 조사 작업이 이어집니다. 추천이 도착하면 자동으로 표시됩니다."
            />
          ) : showCandidates ? (
            <div className="lab-fixture-candidates">
              <CandidateCard
                media={{ alt: "27인치 업무용 모니터", src: monitorImage }}
                merchant="Example Store US"
                name="27인치 USB-C 90W 업무용 모니터"
                price="350.00 USD"
                recommendation="예산 안에서 노트북 충전과 높이 조절 조건을 모두 만족합니다."
                optionSummary="Black · 높이 조절 스탠드"
                state="pinned"
                iconActions={[
                  { kind: "pin", label: "PIN 해제", pressed: true },
                  { kind: "like", label: "좋아요", pressed: false },
                  { kind: "dislike", label: "싫어요", pressed: false },
                ]}
                primaryAction={{ label: "장바구니 담기" }}
                details={<p>PIN 상태와 관찰 근거를 확인합니다.</p>}
              />
              <CandidateCard
                media={{ alt: "휴대용 모니터", state: "missing" }}
                merchant="Example Market"
                name="16인치 휴대용 USB-C 모니터"
                price="189.00 USD"
                recommendation="가볍지만 고정 업무 환경에는 우선순위가 낮습니다."
                optionSummary="Gray · 보호 커버 포함"
                iconActions={[
                  { kind: "pin", label: "PIN", pressed: false },
                  { kind: "like", label: "좋아요", pressed: false },
                  { kind: "dislike", label: "싫어요", pressed: false },
                ]}
                primaryAction={{ label: "장바구니 담기" }}
                details={<p>이미지 없이도 비교와 구매를 계속할 수 있습니다.</p>}
              />
            </div>
          ) : (
            <Notice>
              Target feedback과 재조사는 이 상품군 안에서만 새 ResearchRound를
              만듭니다.
            </Notice>
          )}
        </article>

        {showExpandedComposer ? (
          <section
            className="lab-curation-expansion"
            aria-labelledby="lab-curation-expansion-title"
          >
            <span
              className="lab-curation-expansion__marker"
              aria-hidden="true"
            >
              +
            </span>
            <div className="lab-curation-expansion__intro">
              <p>큐레이션 확장</p>
              <h3 id="lab-curation-expansion-title">조사할 항목 추가</h3>
              <span>
                새 상품군 정의부터 첫 Candidate 조사까지 한 행동으로 이어집니다.
              </span>
            </div>
            <Field
              id="lab-curation-expansion-input"
              label="추가할 항목"
              hint="한 번에 여러 상품군을 요청해도 됩니다."
            >
              <Textarea
                rows={3}
                placeholder="추가로 조사할 상품이나 항목을 입력하세요"
              />
            </Field>
            <div className="lab-curation-expansion__actions">
              <Button emphasis="quiet">닫기</Button>
              <Button emphasis="primary">새 항목 조사</Button>
            </div>
          </section>
        ) : (
          <Button
            className="lab-curation-add-trigger"
            emphasis="quiet"
            type="button"
            aria-expanded="false"
            aria-controls="lab-curation-expansion-input"
          >
            <span aria-hidden="true">+</span>
            <span className="vt-visually-hidden">
              조사할 상품이나 항목 추가
            </span>
          </Button>
        )}
      </div>
    </section>
  );
}

function CartFixture({ state }: { state: FixtureState }) {
  const returning = state === "return";

  return (
    <section
      className="lab-fixture-body lab-cart-fixture"
      data-scenario-state={state}
      aria-label="장바구니 상태 fixture"
    >
      {returning && (
        <Notice
          title="내가 해야 할 결제 행동을 마쳤습니다"
          action={
            <Button emphasis="primary">장바구니로 돌아가기</Button>
          }
        >
          Vitlane이 TEST 주문 처리 결과를 계속 추적합니다. 남은 상품군은
          장바구니에서 이어서 구매할 수 있습니다.
        </Notice>
      )}

      <div className="lab-cart-fixture__layout">
        <header>
          <div>
            <p>Plan shopping cart</p>
            <h2>업무 공간 준비</h2>
          </div>
          <strong>{state === "partial" || returning ? "일부 구매" : "구매 준비"}</strong>
        </header>
        <OrderSummary
          headingLevel="h3"
          title="27인치 USB-C 업무용 모니터"
          rows={[
            {
              label: "선택 옵션",
              value: "Black · 높이 조절 스탠드",
            },
            { label: "수량", value: "1" },
            { label: "결제 예정", value: "350.000000 tVITUSD" },
          ]}
          boundary={
            <Notice tone="test">
              이 상품의 AgencyOrder는 다른 상품군과 독립적으로 진행됩니다.
            </Notice>
          }
        />
        {!returning && (
          <div className="lab-cart-fixture__actions">
            <Button emphasis="quiet">장바구니에서 제거</Button>
            <Button emphasis="primary">
              {state === "partial" ? "남은 상품 구매" : "이 상품 구매"}
            </Button>
          </div>
        )}
      </div>
    </section>
  );
}
