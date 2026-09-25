import { AnalyticsConsent } from "../shared/analytics/AnalyticsConsent";
import { analyticsChanged, startAnalytics, track } from "../shared/analytics/analytics";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { ArrowRight, Menu, ReceiptText } from "lucide-react";
import type { IconType } from "react-icons";
import {
  SiPaypal,
  SiShopify,
} from "react-icons/si";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  ReededGlass,
} from "../shared/ui";
import "./marketing-stack.css";
import { isReleaseBuild, publishableVoices, type EarlyVoice } from "./content/reviews";
import { laneCamera, laneCameraRest, laneSpotThroughCamera, type LaneSpot } from "./laneCamera";

const marketingLocale = window.location.pathname === "/ko" || window.location.pathname.startsWith("/ko/")
  ? "ko-KR"
  : "en-US";
const ml = (english: string, korean: string) => marketingLocale === "ko-KR" ? korean : english;
const marketingPath = (path: string) => marketingLocale === "ko-KR" ? `/ko${path}` : path;
document.documentElement.lang = marketingLocale === "ko-KR" ? "ko" : "en";
// Release smoke reads this to know whether stub content may appear (N8).
document.documentElement.dataset.releaseBuild = String(isReleaseBuild);

const products = [
  {
    name: "Daily Trainer",
    seller: "Northline",
    price: ml("90.00 USD", "90.00 USD"),
    image: "/assets/products/optimized/daily-trainer.webp",
    alt: ml("Navy sample daily training shoe", "네이비 샘플 데일리 트레이닝화"),
    fit: ml("Low stack · stable support", "낮은 스택 · 안정적인 지지"),
    reason: ml("The lowest-priced option that meets the stability and durability needs of daily training.", "매일 훈련에 필요한 안정성과 내구성을 가장 낮은 가격으로 충족합니다."),
  },
  {
    name: "Knit Runner",
    seller: "Openstep",
    price: "135.00 USD",
    image: "/assets/products/optimized/knit-runner.webp",
    alt: ml("Light sample knit running shoe", "밝은 컬러 샘플 니트 러닝화"),
    fit: ml("Wide toe box · zero drop", "넓은 토박스 · 제로 드롭"),
    reason: ml("A strong alternative when toe room and flexibility come first.", "발가락 공간과 유연성을 우선할 때 좋은 대안입니다."),
  },
  {
    name: "Cross Trainer",
    seller: "Fieldwork",
    price: "139.00 USD",
    image: "/assets/products/optimized/cross-trainer.webp",
    alt: ml("Charcoal sample cross-training shoe", "차콜 컬러 샘플 크로스 트레이닝화"),
    fit: ml("Running & HIIT · strong grip", "러닝·HIIT · 강한 접지"),
    reason: ml("A good fit for combining running and strength training in one pair.", "달리기와 근력 운동을 한 켤레로 병행할 때 적합합니다."),
  },
  {
    name: "All-day Walker",
    seller: "Waypoint",
    price: "120.00 USD",
    image: "/assets/products/optimized/allday-walker.webp",
    alt: ml("Light gray sample all-day walking shoe", "라이트 그레이 샘플 올데이 워킹화"),
    fit: ml("All day · cushioned", "하루 종일 · 쿠셔닝"),
    reason: ml("A balanced option for both workouts and everyday movement.", "운동과 일상 이동을 함께 고려한 균형형 후보입니다."),
  },
  {
    name: "Road Runner",
    seller: "Paceworks",
    price: "100.00 USD",
    image: "/assets/products/optimized/road-runner.webp",
    alt: ml("Black sample road running shoe", "블랙 컬러 샘플 로드 러닝화"),
    fit: ml("Daily running · impact absorption", "데일리 러닝 · 충격 흡수"),
    reason: ml("A daily running option with a good balance of price and cushioning.", "가격과 쿠셔닝의 균형이 좋은 데일리 러닝 후보입니다."),
  },
  {
    name: "Cushioned Runner",
    seller: "Softstride",
    price: "131.00 USD",
    image: "/assets/products/optimized/cushioned-runner.webp",
    alt: ml("Blue sample cushioned running shoe", "블루 컬러 샘플 쿠셔닝 러닝화"),
    fit: ml("Soft landing · road running", "부드러운 착지 · 로드 러닝"),
    reason: ml("An option focused on a soft landing and natural transition.", "부드러운 착지감과 자연스러운 전환을 우선한 후보입니다."),
  },
] as const;

// The first-viewport title (2026-09-18): four conditions take turns in one
// slot over a fixed result clause. The heading's accessible name reads every
// condition once, so the rotation never re-announces.
// The key words carry the Vitlane accent (owner 2026-09-18); brackets mark
// them in the copy and never reach the screen or the accessible name.
const heroConditions = [
  ml("[First day] at work?", "[첫 출근]이어도,"),
  ml("On a [tight budget]?", "[절약]이 필요해도,"),
  ml("Particular [taste]?", "세심한 [취향]도,"),
  ml("[Buying on repeat]?", "[반복적인 구매]도,"),
] as const;
const plainCondition = (condition: string) => condition.replace(/[[\]]/g, "");
const heroResult = ml("You still buy better.", "더 잘 삽니다.");
const heroTitleName = `${heroConditions.map(plainCondition).join(" ")} ${heroResult}`;
const rotationPeriodMs = 2000;

type BrandMention = { id: string; label: string; Icon?: IconType };

// Public marketing must describe the checked-in production default. Optional
// Amazon and Korean providers appear in the product only after the server
// reports that their credential, feature and operator gates are all active.
const sourceStores: readonly BrandMention[] = [
  { id: "shopify", label: ml("Shopify", "Shopify"), Icon: SiShopify },
];

// The current-scope section names payment pilots without claiming that any
// live rail is active. The subtitle and body state the activation boundary.
const payMethods: readonly BrandMention[] = [
  { id: "paypal", label: ml("PayPal", "PayPal"), Icon: SiPaypal },
  { id: "giwa", label: ml("GIWA", "GIWA") },
  { id: "usdc", label: ml("USDC", "USDC") },
];

// Both hooks read the query on the first render so a phone never paints the
// variant it is about to leave.
function useReducedMotion() {
  const [reduced, setReduced] = useState(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  useEffect(() => {
    const query = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(query.matches);
    update();
    query.addEventListener?.("change", update);
    return () => query.removeEventListener?.("change", update);
  }, []);
  return reduced;
}

function useMediaQuery(query: string) {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const list = window.matchMedia(query);
    const update = () => setMatches(list.matches);
    update();
    list.addEventListener?.("change", update);
    return () => list.removeEventListener?.("change", update);
  }, [query]);
  return matches;
}

// ADR-0078: the first viewport sits behind reeded glass. The light gathers
// behind the product action and swells slowly; the component pauses offscreen,
// in a hidden tab and under reduced motion, and follows the header theme.
export function LivingLane() {
  return (
    <div className="living-lane">
      <ReededGlass anchor=".hero-product-link" motion="swell" />
    </div>
  );
}

// A rotation index that advances every period while the tab is visible and
// stays on the first item under reduced motion.
function useRotation(length: number, periodMs: number) {
  const [index, setIndex] = useState(0);
  const reducedMotion = useReducedMotion();

  useEffect(() => {
    if (reducedMotion) return;
    const interval = window.setInterval(() => {
      if (!document.hidden) setIndex((current) => (current + 1) % length);
    }, periodMs);
    return () => window.clearInterval(interval);
  }, [length, periodMs, reducedMotion]);

  return reducedMotion ? 0 : index;
}

// The incoming word rises briefly into place; mount it with a new key to replay.
function RotatingWord({ children, className }: { children: ReactNode; className: string }) {
  const wordRef = useRef<HTMLSpanElement>(null);

  useLayoutEffect(() => {
    const word = wordRef.current;
    if (!word || typeof word.animate !== "function") return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const animation = word.animate(
      [
        { opacity: 0, transform: "translateY(0.16em) scale(0.985)" },
        { opacity: 1, transform: "none" },
      ],
      { duration: 260, easing: "ease-out", fill: "both" },
    );
    return () => animation.cancel();
  }, []);

  return (
    <span className={className} ref={wordRef}>
      {children}
    </span>
  );
}

function ConditionText({ condition }: { condition: string }) {
  const [lead, key, tail] = condition.split(/[[\]]/);
  return (
    <>
      {lead}
      <span className="hero-title-key">{key}</span>
      {tail}
    </>
  );
}

export function MarketingThesis() {
  const conditionIndex = useRotation(heroConditions.length, rotationPeriodMs);

  return (
    <div className="hero-thesis-block">
      <div className="hero-thesis-copy">
        <h1 id="hero-title">
          <span className="sr-only">{heroTitleName}</span>
          <span aria-hidden="true" className="hero-title-visual">
            {/* Invisible copies of every condition keep the slot as large as the
                longest one, so the result clause never moves. */}
            <span className="hero-title-condition">
              <RotatingWord className="hero-title-live" key={`live-${conditionIndex}`}>
                <ConditionText condition={heroConditions[conditionIndex]} />
              </RotatingWord>
              {heroConditions.map((condition, index) => (
                <span className="hero-title-ghost" key={`ghost-${index}`}>
                  <ConditionText condition={condition} />
                </span>
              ))}
            </span>
            <span className="hero-title-result">{heroResult}</span>
          </span>
        </h1>
        <p className="hero-thesis">
          {ml("Research, compare, and stay on budget.", "조사, 비교, 예산 맞춤까지.")}
        </p>
      </div>
      <div className="hero-actions">
        <a className="hero-product-link" data-vitlane-app-path="/" href={appHref("/")}>
          <span>{ml("Open the product", "제품 가기")}</span>
          <span aria-hidden="true">→</span>
        </a>
        <div className="hero-secondary-actions">
          <a
            className="hero-social-link"
            href="https://x.com/Vitlane_"
            target="_blank"
            rel="noopener noreferrer"
          >
            <span>{ml("See on X", "X에서 보기")}</span>
            <span className="sr-only">
              {ml("(opens in a new tab)", "(새 탭에서 열림)")}
            </span>
          </a>
        </div>
      </div>
    </div>
  );
}

function BrandMentionWord({ brand, className }: { brand: BrandMention; className: string }) {
  const Icon = brand.Icon;
  return (
    <span className={`${className} ${className}--${brand.id}`}>
      {Icon ? <Icon aria-hidden={true} /> : null}
      <span>{brand.label}</span>
    </span>
  );
}

// The Searching band hangs at the bottom of the first viewport inside the
// reeded glass. The names drift left in one slow loop; the second copy is
// only for the loop and reduced motion drops it.
export function SourceBand() {
  const reducedMotion = useReducedMotion();
  const renderList = (clone: boolean) => (
    <ul
      aria-hidden={clone ? true : undefined}
      className="source-band-list"
      data-clone={clone ? "true" : undefined}
    >
      {sourceStores.map((store) => (
        <li key={store.id}>
          <BrandMentionWord brand={store} className="source-band-store" />
        </li>
      ))}
    </ul>
  );

  return (
    <div className="source-band-inner">
      {/* No visible label (owner 2026-09-18); the section keeps a spoken name. */}
      <p className="sr-only" id="source-band-title">
        {ml("Current catalog source", "현재 카탈로그 소스")}
      </p>
      <div className="source-band-viewport">
        <div className="source-band-track">
          {renderList(false)}
          {reducedMotion ? null : renderList(true)}
        </div>
      </div>
    </div>
  );
}

export function PaymentPilotTitle() {
  const methodIndex = useRotation(payMethods.length, rotationPeriodMs);
  const method = payMethods[methodIndex];

  return (
    <>
      <span className="sr-only">
        {ml(
          "Gated payment pilots: PayPal, GIWA, and USDC. Activation required.",
          "승인이 필요한 결제 파일럿: PayPal, GIWA, USDC. 활성화가 필요합니다.",
        )}
      </span>
      <span aria-hidden="true" className="pay-with">
        <span className="pay-with-lead">{ml("Gated pilot", "승인 필요")}</span>
        <span className="pay-with-slot">
          <RotatingWord className="pay-with-live" key={`live-${method.id}`}>
            <BrandMentionWord brand={method} className="pay-method" />
          </RotatingWord>
          {payMethods.map((item) => (
            <span className="pay-with-ghost" key={`ghost-${item.id}`}>
              <BrandMentionWord brand={item} className="pay-method" />
            </span>
          ))}
        </span>
      </span>
    </>
  );
}

// ---- One Lane (ADR-0074): "왜 Vitlane인가" told once, as six moments on the real screens ----


type LaneMoment = {
  id: string;
  label: string;
  title: string;
  body: string;
  agent: string | null;
  // 1-based frame under marketing/assets/lane/{en,ko}; null renders the TEST receipt.
  frame: number | null;
  // The evidence for the claim, in percent of the 1200×760 frame; null lights the whole frame.
  spot: LaneSpot | null;
  alt: string;
};

const laneFrameCount = 5;

// The same request represented by the source-owned lane illustrations.
const laneRequest = ml(
  "Training shoes I can wear every day. Budget $120, wide fit and stable for lifting.",
  "매일 신을 트레이닝화를 찾아줘. 예산은 120달러, 발볼이 넓고 리프팅할 때 안정적인 걸로.",
);

const laneMoments: readonly LaneMoment[] = [
  {
    id: "say",
    label: ml("Request", "요청"),
    title: ml("Say it, we find it.", "말하면 찾습니다"),
    body: ml(
      "No product names needed. Use, budget and conditions in one sentence.",
      "상품명을 몰라도 됩니다. 용도, 예산, 조건을 한 문장으로.",
    ),
    agent: null,
    frame: 1,
    spot: { left: 26.5, top: 47.5, width: 53, height: 6 },
    alt: ml("The Vitlane composer with the request typed in", "요청을 입력한 Vitlane 입력 화면"),
  },
  {
    id: "research",
    label: ml("Research", "조사"),
    title: ml("Vitlane does the research.", "조사는 Vitlane이 합니다"),
    body: ml(
      "The request becomes research axes and Vitlane sweeps the catalog. Nothing for you to do meanwhile.",
      "요청을 조사 축으로 나누고 카탈로그를 훑습니다. 그동안 할 일은 없습니다.",
    ),
    agent: ml("Checking price, fit for use, and delivery.", "가격, 용도, 배송 가능 여부를 확인하고 있어요."),
    frame: 2,
    spot: { left: 26, top: 47.5, width: 33, height: 8.5 },
    alt: ml("A research target while Vitlane is still researching", "Vitlane이 아직 조사 중인 조사 대상 화면"),
  },
  {
    id: "ruler",
    label: ml("Compare", "비교"),
    title: ml("Compared on one ruler.", "같은 기준으로 비교합니다"),
    body: ml(
      "Every candidate shows axis scores, price and reasoning in the same place.",
      "후보마다 축별 점수, 가격, 근거가 같은 자리에 놓입니다.",
    ),
    agent: ml("Three candidates, on one ruler.", "같은 기준으로 후보 3개만 남겼어요."),
    frame: 3,
    spot: { left: 27, top: 60.2, width: 61.5, height: 5.8 },
    alt: ml("Candidates compared side by side with price and reasoning", "가격과 근거를 같은 자리에 놓고 비교하는 후보 화면"),
  },
  {
    id: "keep",
    label: ml("Choose", "선택"),
    title: ml("Budget fit comes first.", "예산에 맞는 후보부터 보여줍니다"),
    body: ml(
      "Budget-fit options lead; compare each candidate's price with your budget.",
      "예산에 맞는 후보를 먼저 보여주고, 각 후보의 가격을 예산과 비교합니다.",
    ),
    agent: ml("Going with Men's Outwork. It fits the budget you approved.", "Men's Outwork로 진행할게요. 승인한 예산 안입니다."),
    frame: 4,
    spot: { left: 31.5, top: 12.4, width: 10, height: 5.6 },
    alt: ml("The chosen candidate with its price against the budget and the axis reasoning", "예산 대비 가격과 축별 근거가 보이는 선택 후보 화면"),
  },
  {
    id: "carry",
    label: ml("TEST flow", "TEST 흐름"),
    title: ml("See the simulated handoff.", "모의 후속 흐름을 확인합니다"),
    body: ml(
      "This demonstration continues with a no-value TEST payment and simulated order. No real purchase occurs.",
      "이 데모는 무가치 TEST 결제와 모의 주문으로 이어집니다. 실제 구매는 발생하지 않습니다.",
    ),
    agent: ml(
      "Paying within the approved 90.00 USD in TEST. Nothing for you to do.",
      "승인한 90.00 USD 안에서 TEST 결제를 진행하고 있어요. 따로 할 일은 없어요.",
    ),
    frame: 5,
    spot: { left: 25.5, top: 15.5, width: 57, height: 7.5 },
    alt: ml("Simulated TEST order progress after the choice", "선택 뒤 모의 TEST 주문 진행 화면"),
  },
  {
    id: "receipt",
    label: ml("Receipt", "영수증"),
    title: ml("One screen to the end.", "끝까지 한 화면에서"),
    body: ml("Progress and the receipt live in the same place.", "진행과 영수증은 같은 자리에서 확인합니다."),
    agent: ml("Done. The simulated TEST receipt is ready.", "완료됐어요. 모의 TEST 영수증을 준비했어요."),
    frame: null,
    spot: null,
    alt: "",
  },
];

const laneFrameSource = (frame: number) =>
  `/assets/lane/${marketingLocale === "ko-KR" ? "ko" : "en"}/0${frame}.svg`;

const laneStageNumber = (index: number) => String(index + 1).padStart(2, "0");

// The receipt is laid out at the frame size it was designed for (41rem wide,
// 1200:760) and scaled to the actual frame, so a phone shows the same ticket.
const laneReceiptDesignWidthRem = 41;


export function LaneExperience() {
  const [stage, setStage] = useState(0);
  const [stageProgress, setStageProgress] = useState(0);
  const sectionRef = useRef<HTMLElement | null>(null);
  // A click or a drag sets the moment directly; scroll-driven updates pause
  // until the page has moved to the matching position.
  const lockUntilRef = useRef(0);
  const dragRef = useRef<{ startX: number; startIndex: number; pointerId: number; step: number } | null>(null);
  const frameRef = useRef<HTMLDivElement | null>(null);
  const reducedMotion = useReducedMotion();
  const stickyRef = useRef<HTMLDivElement | null>(null);
  // Phones run the same sticky scene in one column (ADR-0076). Reduced motion,
  // viewports too short for a scene (landscape phones) and a phone scene that
  // does not fit (enlarged text) get the vertical list; only one of the two is
  // in the DOM, so no hidden media.
  const narrow = useMediaQuery("(max-width: 48rem)");
  const [phoneSceneOverflows, setPhoneSceneOverflows] = useState(false);
  const stacked = useMediaQuery("(max-height: 30rem)") || reducedMotion || (narrow && phoneSceneOverflows);

  // A new viewport width (rotation) gets a fresh chance to fit the scene. Height
  // alone changes while mobile browser bars slide and must not swap variants.
  useEffect(() => {
    let width = window.innerWidth;
    const retry = () => {
      if (window.innerWidth === width) return;
      width = window.innerWidth;
      setPhoneSceneOverflows(false);
    };
    window.addEventListener("resize", retry);
    return () => window.removeEventListener("resize", retry);
  }, []);

  useLayoutEffect(() => {
    const sticky = stickyRef.current;
    if (stacked || !narrow || !sticky) return;
    // The scene does not fit when its content is clipped or the frame had to
    // give up more than half the width (enlarged text, very short phones).
    const check = () => {
      const grid = sticky.querySelector<HTMLElement>(".lane-grid");
      const frame = sticky.querySelector<HTMLElement>(".lane-frame");
      const clipped = sticky.scrollHeight > sticky.clientHeight + 2 ||
        (grid !== null && grid.scrollHeight > grid.clientHeight + 2);
      const frameTooSmall = grid !== null && frame !== null && frame.clientWidth < grid.clientWidth * 0.5;
      if (clipped || frameTooSmall) setPhoneSceneOverflows(true);
    };
    check();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(check);
    for (const child of Array.from(sticky.querySelectorAll(".lane-heading, .lane-list, .lane-talk, .lane-frame"))) {
      observer?.observe(child);
    }
    return () => observer?.disconnect();
  }, [narrow, stacked]);

  useEffect(() => {
    const frame = frameRef.current;
    if (stacked || !frame) return;
    const measure = () => {
      const rem = parseFloat(window.getComputedStyle(document.documentElement).fontSize) || 16;
      frame.style.setProperty(
        "--lane-frame-scale",
        String(frame.clientWidth / (laneReceiptDesignWidthRem * rem)),
      );
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(frame);
    return () => observer?.disconnect();
  }, [stacked]);

  useEffect(() => {
    sectionRef.current = document.querySelector<HTMLElement>("#lane");
    let frame = 0;
    const update = () => {
      frame = 0;
      const section = sectionRef.current;
      if (!section) return;
      if (Date.now() < lockUntilRef.current) return;
      const bounds = section.getBoundingClientRect();
      const travel = Math.max(section.offsetHeight - window.innerHeight, 1);
      const progress = Math.min(Math.max(-bounds.top / travel, 0), 1);
      const scaled = Math.min(progress * laneMoments.length, laneMoments.length - 0.001);
      setStage(Math.floor(scaled));
      setStageProgress(scaled - Math.floor(scaled));
    };
    const requestUpdate = () => {
      if (!frame) frame = window.requestAnimationFrame(update);
    };
    update();
    window.addEventListener("scroll", requestUpdate, { passive: true });
    window.addEventListener("resize", requestUpdate);
    return () => {
      window.removeEventListener("scroll", requestUpdate);
      window.removeEventListener("resize", requestUpdate);
      if (frame) window.cancelAnimationFrame(frame);
    };
  }, []);

  const jumpTo = (index: number, smooth = !reducedMotion) => {
    const next = Math.min(Math.max(index, 0), laneMoments.length - 1);
    setStage(next);
    setStageProgress(1);
    const section = sectionRef.current;
    if (!section) return;
    lockUntilRef.current = Date.now() + (smooth ? 900 : 250);
    const travel = Math.max(section.offsetHeight - window.innerHeight, 1);
    const top = window.scrollY + section.getBoundingClientRect().top;
    // "auto" would inherit the page's smooth scrolling and outlast the lock.
    window.scrollTo({
      top: top + (travel * (next + 0.5)) / laneMoments.length,
      behavior: smooth ? "smooth" : "instant",
    });
  };

  // Horizontal drag on the frame scrubs through the moments (120px a step,
  // a fifth of the frame on a phone). A drag never starts on the receipt, so
  // its link keeps a plain click.
  const onFramePointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    if (event.target instanceof Element && event.target.closest(".receipt-ticket")) return;
    const step = Math.max(48, Math.min(120, event.currentTarget.clientWidth / 5));
    dragRef.current = { startX: event.clientX, startIndex: stage, pointerId: event.pointerId, step };
    event.currentTarget.setPointerCapture(event.pointerId);
  };
  const onFramePointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const next = Math.min(
      Math.max(drag.startIndex + Math.round((event.clientX - drag.startX) / drag.step), 0),
      laneMoments.length - 1,
    );
    if (next !== stage) jumpTo(next, false);
  };
  const onFramePointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId === event.pointerId) dragRef.current = null;
  };

  const heading = ml("Why Vitlane", "왜 Vitlane인가");

  if (stacked) {
    return (
      <div className="lane-stack">
        <div className="lane-stack-heading">
          <h2 id="lane-title">{heading}</h2>
        </div>
        {laneMoments.map((moment, index) => (
          <figure key={moment.id}>
            {moment.frame ? (
              <img alt={moment.alt} decoding="async" loading="lazy" width={1200} height={760} src={laneFrameSource(moment.frame)} />
            ) : (
              <div className="lane-stack-receipt"><Receipt /></div>
            )}
            <figcaption>
              <small>{laneStageNumber(index)} · {moment.label}</small>
              <strong>{moment.title}</strong>
              <span>{moment.body}</span>
              {moment.agent ? <em>{moment.agent}</em> : null}
            </figcaption>
          </figure>
        ))}
      </div>
    );
  }

  const active = laneMoments[stage];
  const camera = narrow ? laneCamera(active.spot) : laneCameraRest;
  const spot = active.spot ? laneSpotThroughCamera(active.spot, camera) : null;
  const sceneStyle = { "--lane-stage": stage } as CSSProperties;
  const cameraStyle = {
    "--lane-camera-x": `${camera.x}%`,
    "--lane-camera-y": `${camera.y}%`,
    "--lane-camera-zoom": camera.zoom,
  } as CSSProperties;
  const typedRequest = stage === 0
    ? laneRequest.slice(0, Math.max(1, Math.ceil(laneRequest.length * stageProgress)))
    : laneRequest;
  const frames = Array.from({ length: laneFrameCount }, (_, index) => index + 1);

  return (
    <div className="lane-sticky" data-stage={stage} ref={stickyRef} style={sceneStyle}>
      <div className="lane-heading">
        <div>
          <p className="utility-label utility-label--inverse">{ml("Scroll to run the lane", "스크롤하면 진행됩니다")}</p>
          <h2 id="lane-title">{heading}</h2>
        </div>
        <div className="lane-progress" aria-hidden="true">
          <span>{laneStageNumber(stage)}</span>
          <div><i className={`lane-progress-fill lane-progress-fill--${stage}`} /></div>
          <span>{laneStageNumber(laneMoments.length - 1)}</span>
        </div>
      </div>
      <div className="lane-grid">
        {/* A phone shows one moment at a time; keyboard focus brings its moment in. */}
        <ol className="lane-list">
          {laneMoments.map((moment, index) => (
            <li key={moment.id}>
              <Button
                aria-current={index === stage ? "step" : undefined}
                className={`lane-item ${index === stage ? "is-active" : ""}`}
                emphasis="quiet"
                onClick={() => jumpTo(index)}
                onFocus={narrow ? () => { if (index !== stage) jumpTo(index); } : undefined}
              >
                <small>{laneStageNumber(index)} · {moment.label}</small>
                <strong>{moment.title}</strong>
                <span>{moment.body}</span>
              </Button>
            </li>
          ))}
        </ol>
        <div className="lane-stage">
          <div
            className="lane-frame"
            ref={frameRef}
            style={cameraStyle}
            onPointerCancel={onFramePointerUp}
            onPointerDown={onFramePointerDown}
            onPointerMove={onFramePointerMove}
            onPointerUp={onFramePointerUp}
          >
            {frames.map((frame) => (
              <img
                alt={active.frame === frame ? active.alt : ""}
                className={active.frame === frame ? "is-active" : undefined}
                decoding="async"
                loading="lazy"
                width={1200}
                height={760}
                key={frame}
                src={laneFrameSource(frame)}
              />
            ))}
            {active.frame === null ? <Receipt /> : null}
            <div
              aria-hidden="true"
              className={`lane-spot ${spot ? "" : "is-hidden"}`}
              style={spot
                ? {
                    left: `${spot.left}%`,
                    top: `${spot.top}%`,
                    width: `${spot.width}%`,
                    height: `${spot.height}%`,
                  }
                : undefined}
            />
          </div>
          <div className="lane-talk">
            <p className="lane-request">
              <span>{typedRequest}</span>
              <span className="typing-caret" aria-hidden="true" />
            </p>
            <p aria-live="polite" className={`lane-agent ${active.agent ? "is-visible" : ""}`}>
              <span className="assistant-avatar" aria-hidden="true">{ml("V", "V")}</span>
              <span>{active.agent ?? ""}</span>
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}

function Receipt() {
  const product = products[0];
  return (
    <div className="receipt-machine">
      <div className="receipt-machine-head">
        <ReceiptText aria-hidden="true" />
        <span>{ml("Vitlane TEST receipt", "Vitlane TEST 영수증")}</span>
        <span>{ml("Issued", "발행됨")}</span>
      </div>
      <article className="receipt-ticket">
        <header>
          <span className="brand-mark__symbol" aria-hidden="true" />
          <strong>{ml("Vitlane", "Vitlane")}</strong>
          <span>{ml("TEST order receipt", "TEST 주문 영수증")}</span>
        </header>
        <div className="receipt-product">
          <img src={product.image} alt={product.alt} loading="lazy" decoding="async" />
          <div><small>{product.seller}</small><strong>{product.name}</strong><span>{ml("Navy · US 10", "네이비 · 280")}</span></div>
        </div>
        <dl>
          <div><dt>{ml("Product amount", "상품 금액")}</dt><dd>{ml("90.00 USD", "90.00 USD")}</dd></div>
          <div><dt>{ml("TEST payment", "TEST 결제")}</dt><dd>{ml("90.00 tVITUSD", "90.00 tVITUSD")}</dd></div>
          <div><dt>{ml("Order processing", "주문 처리")}</dt><dd>{ml("Simulated order · SIMULATED", "모의 주문 처리 · SIMULATED")}</dd></div>
          <div className="receipt-total"><dt>{ml("Total", "합계")}</dt><dd>{ml("90.00 USD", "90.00 USD")}</dd></div>
        </dl>
        <p>{ml("No real product order, delivery, or transfer of an asset with value occurred.", "실제 상품 주문·배송 또는 가치 있는 자산 이동은 발생하지 않았습니다.")}</p>
        <a className="receipt-cta" data-vitlane-app-path="/" href={appHref("/")}>
          {ml("Open Vitlane", "Vitlane 열기")} <ArrowRight aria-hidden="true" />
        </a>
      </article>
    </div>
  );
}

export function ProductGallery() {
  const rootRef = useRef<HTMLDivElement>(null);
  const [progress, setProgress] = useState(0);
  useEffect(() => {
    let frame = 0;
    const update = () => {
      frame = 0;
      const root = rootRef.current;
      if (!root) return;
      const section = root.closest<HTMLElement>(".gallery-section");
      if (!section) return;
      const rect = section.getBoundingClientRect();
      const travel = Math.max(section.offsetHeight - window.innerHeight, 1);
      const next = Math.min(Math.max(-rect.top / travel, 0), 1);
      setProgress(next);
    };
    const requestUpdate = () => {
      if (!frame) frame = window.requestAnimationFrame(update);
    };
    update();
    window.addEventListener("scroll", requestUpdate, { passive: true });
    window.addEventListener("resize", requestUpdate);
    return () => {
      window.removeEventListener("scroll", requestUpdate);
      window.removeEventListener("resize", requestUpdate);
      if (frame) window.cancelAnimationFrame(frame);
    };
  }, []);

  const progressStep = Math.round(progress * 10);

  return (
    <div
      className={`product-gallery product-gallery--step-${progressStep}`}
      data-progress-step={progressStep}
      ref={rootRef}
    >
      <div className="gallery-copy">
        <p className="utility-label utility-label--inverse">{ml("Real examples from the Shopify catalog", "Shopify 카탈로그 실제 예시")}</p>
        <h2 id="gallery-title">{ml("Looking for something?", "찾고 싶은 상품이 있나요?")}</h2>
        <p>{ml("You don't need to know the product name. Just tell us what you need.", "상품명을 몰라도 좋습니다. 뭐든 말해주세요.")}</p>
        <a className="gallery-cta" data-vitlane-app-path="/" href={appHref("/")}>
          {ml("Get started", "제품 시작하기")} <ArrowRight aria-hidden="true" />
        </a>
      </div>
      <div className="gallery-perspective" aria-label={ml("Shopify product examples", "Shopify 상품 예시")}>
        {products.map((product, index) => (
          <article className={`gallery-card gallery-card--${index}`} key={product.name}>
            <img src={product.image} alt={product.alt} loading="lazy" decoding="async" />
            <div><span>{product.seller}</span><strong>{product.name}</strong><small>{product.price}</small></div>
          </article>
        ))}
      </div>
      <p className="catalog-source">{ml("2026-08-12 Shopify Global Catalog read-only search snapshot · verify current price and availability with the seller", "2026-08-12 Shopify Global Catalog 읽기 전용 검색 snapshot · 현재 가격과 재고는 판매처에서 확인")}</p>
    </div>
  );
}

function appHref(path: string) {
  const localMarketingHosts = new Set([
    "127.0.0.1",
    "host.docker.internal",
    "localhost",
    "marketing.localhost",
    "::1",
  ]);
  if (!localMarketingHosts.has(window.location.hostname)) {
    return new URL(path, "https://app.vitlane.com").href;
  }
  const hostname = window.location.hostname === "marketing.localhost"
    ? "127.0.0.1"
    : window.location.hostname;
  const origin = `${window.location.protocol}//${hostname}${
    window.location.port ? `:${window.location.port}` : ""
  }`;
  return new URL(path, origin).href;
}

const livingLaneRoot = document.querySelector("#living-lane-root");
if (livingLaneRoot) createRoot(livingLaneRoot).render(<LivingLane />);

const thesisRoot = document.querySelector("#marketing-thesis-root");
if (thesisRoot) createRoot(thesisRoot).render(<MarketingThesis />);

const sourceBandRoot = document.querySelector("#marketing-source-band-root");
if (sourceBandRoot) createRoot(sourceBandRoot).render(<SourceBand />);

const paymentPilotRoot = document.querySelector("#marketing-payment-pilot-root");
if (paymentPilotRoot) createRoot(paymentPilotRoot).render(<PaymentPilotTitle />);

const laneRoot = document.querySelector("#marketing-lane-root");
if (laneRoot) createRoot(laneRoot).render(<LaneExperience />);

// Vite folds this branch at build time. Keeping the placeholder literals
// inside the explicit development branch prevents invented testimonials from
// being shipped in a release asset, even when the section is not rendered.
const earlyVoices: readonly EarlyVoice[] = import.meta.env.VITE_ALLOW_DEV_AUTH_UI === "true" ? [
  {
    id: "voice-1",
    quote: ml(
      "I used to open twenty tabs for one pair of running shoes. I typed the use and the budget, got three options with the reasons next to them, and was done in five minutes.",
      "러닝화 하나 사려고 탭을 스무 개 열던 일이 없어졌어요. 용도랑 예산만 적었는데 세 켤레로 정리해 줬고, 왜 골랐는지가 옆에 있어서 5분도 안 걸렸어요.",
    ),
    attribution: ml("Kim · August 2026", "김○○ · 2026년 8월"),
    stub: true,
  },
  {
    id: "voice-2",
    quote: ml(
      "Not having to build the comparison myself was the biggest thing. Price, reasoning and options sit in the same place, so my eyes stop wandering.",
      "비교표를 제가 만들 필요가 없다는 게 제일 컸어요. 가격, 근거, 옵션이 같은 자리에 있어서 눈이 안 흩어져요.",
    ),
    attribution: ml("J. Park · September 2026", "J. Park · 2026년 9월"),
    stub: true,
  },
  {
    id: "voice-3",
    quote: ml(
      "The options that fit my budget came first, and every card showed how its price compared with my limit.",
      "예산에 맞는 후보가 먼저 나왔고, 모든 카드에서 한도와 가격을 바로 비교할 수 있었어요.",
    ),
    attribution: ml("Lee · September 2026", "이○○ · 2026년 9월"),
    stub: true,
  },
  {
    id: "voice-4",
    quote: ml(
      "I gave it a gift budget and the person's taste in one sentence. The options came back with the reasons, and I did not have to explain myself twice.",
      "선물 예산이랑 그 사람 취향을 한 문장으로만 적었어요. 이유가 붙은 후보가 돌아왔고, 같은 설명을 두 번 할 일이 없었어요.",
    ),
    attribution: ml("Choi · September 2026", "최○○ · 2026년 9월"),
    stub: true,
  },
  {
    id: "voice-5",
    quote: ml(
      "After I picked, the payment and the order just proceeded. I checked the receipt the next morning and that was the whole job.",
      "고르고 나니 결제랑 주문이 그냥 진행됐어요. 다음 날 아침에 영수증만 확인했고, 그게 제가 한 일의 전부였어요.",
    ),
    attribution: ml("S. Han · September 2026", "S. Han · 2026년 9월"),
    stub: true,
  },
] : [];

// Three or more quotes drift as one slow horizontal loop; fewer stay as a
// static grid so a lone real quote never marches alone.
export const voicesLoopMinimum = 3;

function Voice({ voice, clone }: { voice: EarlyVoice; clone?: boolean }) {
  return (
    <blockquote
      aria-hidden={clone ? "true" : undefined}
      className="voice"
      data-clone={clone ? "true" : undefined}
      data-stub={voice.stub ? "true" : undefined}
    >
      <p>{voice.quote}</p>
      <footer>{voice.attribution}</footer>
    </blockquote>
  );
}

export function EarlyVoices() {
  const voices = publishableVoices(earlyVoices);
  if (voices.length === 0) return null;
  const loop = voices.length >= voicesLoopMinimum;
  return (
    <div className="voices">
      <h2 id="voices-title">{ml("Early users", "먼저 써 본 사람들")}</h2>
      {loop ? (
        <div className="voices-viewport">
          <div className="voices-track">
            {voices.map((voice) => <Voice key={voice.id} voice={voice} />)}
            {voices.map((voice) => <Voice clone key={`${voice.id}-clone`} voice={voice} />)}
          </div>
        </div>
      ) : (
        <div className="voices-grid">
          {voices.map((voice) => <Voice key={voice.id} voice={voice} />)}
        </div>
      )}
    </div>
  );
}

const voicesRoot = document.querySelector("#marketing-voices-root");
if (voicesRoot) createRoot(voicesRoot).render(<EarlyVoices />);

const galleryRoot = document.querySelector("#marketing-gallery-root");
if (galleryRoot) createRoot(galleryRoot).render(<ProductGallery />);

const mobileMenuRoot = document.querySelector("#marketing-mobile-menu-root");
if (mobileMenuRoot) createRoot(mobileMenuRoot).render(<MarketingMobileMenu />);

function MarketingMobileMenu() {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          aria-label={ml("Mobile menu", "모바일 메뉴")}
          className="marketing-mobile-menu-trigger"
          emphasis="quiet"
          size="compact"
        >
          <Menu aria-hidden="true" />
          <span>{ml("Menu", "메뉴")}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        className="marketing-mobile-menu"
        onKeyDown={(event) => event.stopPropagation()}
      >
        <DropdownMenuItem asChild>
          <a href={marketingPath("/terms/")}>{ml("Service & payment terms", "이용·결제 기준")}</a>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const analyticsRoot = document.querySelector("#marketing-analytics-root");
if (analyticsRoot) {
  createRoot(analyticsRoot).render(<AnalyticsConsent l={ml} showSettings />);
  const page = () => track({ name: "page_view", screen: "landing" }, "marketing-page");
  window.addEventListener(analyticsChanged, page);
  document.addEventListener("visibilitychange", page);
  void startAnalytics("marketing", marketingLocale).then(page);
}
