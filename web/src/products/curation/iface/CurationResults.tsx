import { Fragment, useEffect, useRef } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Button, Popover, PopoverContent, PopoverTrigger, ProductMedia, type CandidateCardAction } from "../../../shared/ui";
import { useLocale, type Localize } from "../../../shared/i18n";
import { productPrice, type CandidatePrice } from "../domain/candidatePresentation";
import { chooseRepresentative, type ComparisonFacts, type RepresentativeReason, type RepresentativeSignal } from "../domain/conversationResults";
import { comparativeStrength, type ResearchCriteria, type ResearchSort } from "../domain/researchCriteria";
import { sourceLabel } from "../domain/sourceLabels";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
import { useConvertedPrice } from "../research/app/useResearchCurrency";
import { CandidateBudgetDelta } from "./BudgetViews";
import { CandidateSourceBadge, candidatePriceLabel } from "./CurationCandidateCard";
import { useTargetOrdering } from "./ResearchComparison";
import { MallLogo } from "./MallLogo";
import "./curation-results.css";

/** Why this product stands for its product group, in the words the sort badges already use. */
function reasonLabel(reason: RepresentativeReason, sort: ResearchSort, criteria: ResearchCriteria | null | undefined, l: Localize) {
  if (sort === "COMBINATION" && reason !== "VIEWED") return l("Combination fit", "조합 어울림");
  if (reason === "CART") return l("In your cart", "담은 상품");
  if (reason === "VIEWED") return l("Last viewed", "마지막으로 본 상품");
  if (reason === "PICK") return l("Pick", "Pick");
  if (sort === "PRICE_ASC") return l("Lowest price", "최저가");
  if (sort === "PRICE_DESC") return l("Highest price", "최고가");
  const axis = criteria?.axes.find(a => `AXIS:${a.axisId}` === sort);
  return axis ? l("#{axis} leader", "#{axis} 1위", { axis: axis.label }) : l("Pick", "Pick");
}

function Score({ product }: { product: LiveCatalogProduct }) {
  const { l } = useLocale();
  const score = product.axisAssessment?.totalScore;
  if (score === undefined) return <span className="curation-result__unscored">{l("Not scored", "미평가")}</span>;
  return <span className="candidate-comparison__score" aria-label={l("Overall score {score} out of 100", "총점 100점 중 {score}점", { score })}><span className="candidate-comparison__value">{score}</span><small>/100</small></span>;
}

/** What a representative shows of its product: the price its card states, and whether it is still being read. */
export type ResultDisplay = { price: CandidatePrice; loading: boolean };

function Price({ product, display }: { product: LiveCatalogProduct; display?: ResultDisplay }) {
  const { l } = useLocale(); const price = display?.price ?? productPrice(product); const converted = useConvertedPrice(price);
  // "Checking" is not "unknown": a saved Shopify product has no price until the page has asked Shopify for it.
  if (display?.loading) return <span className="curation-result__price"><span className="curation-result__pending">{l("Checking price", "가격 확인 중")}</span></span>;
  // A narrow row wraps between the price and its conversion, and inside a price range only at the dash; the short
  // conversion ("약 34,098₩") always stays whole.
  const parts = [candidatePriceLabel(price, l), converted].filter(Boolean);
  return <span className="curation-result__price"><strong>{parts.map((part, index) => <Fragment key={index}>{index > 0 && " · "}<span className={`curation-result__price-part${part === converted ? " is-converted" : ""}`}>{part}</span></Fragment>)}</strong><CandidateBudgetDelta price={price} /></span>;
}

export type ResultTargetProps = {
  layout: "card" | "row";
  targetId: string;
  title: string;
  /** The Target's eligible candidates (visible, not disliked, details resolved) and its whole pool. */
  products: LiveCatalogProduct[];
  cohort: LiveCatalogProduct[];
  /** How many candidates the Target's sheet holds. */
  count: number;
  criteria?: ResearchCriteria | null;
  /** The sort the reader chose for this Target and what they last opened (this device). */
  sort: ResearchSort;
  signal?: RepresentativeSignal;
  combinationCandidateId?: string;
  onRepresentative?: (targetId:string,candidateId?:string)=>void;
  /** When a research last added candidates to this Target. */
  researchedAt?: string;
  inCart: (candidateId: string) => boolean;
  /** The cart action of a candidate Vitlane can check out; undefined for a product bought on its own site. */
  cartAction: (product: LiveCatalogProduct) => CandidateCardAction | undefined;
  /** The card-side presentation of a product the research surface owns (Shopify); other products state their own price. */
  display?: (product: LiveCatalogProduct) => ResultDisplay | undefined;
  priceOf?: (product: LiveCatalogProduct) => CandidatePrice;
  onOpenCandidate: (product: LiveCatalogProduct) => void;
  /** Opens the Target's sheet: criteria, sort and every candidate. */
  onOpenSheet: () => void;
  /** Shown in place of a representative while the Target has no candidate to show. */
  status: string;
};

/**
 * One Target inside a response (ADR-0086): the product that stands for it and the way into its sheet.
 * A response with one Target gives it a card; several Targets take one row each. Nothing opens inside
 * the conversation — the sheet is the Target's own surface.
 */
export function ResultTarget(props: ResultTargetProps) {
  const { l } = useLocale();
  const { effectiveSort, order } = useTargetOrdering(props.cohort, props.criteria, props.sort, props.targetId, props.priceOf, props.combinationCandidateId);
  const ordered = order(props.products);
  const representative = chooseRepresentative({ ordered, inCart: props.inCart, pickSort: effectiveSort === "PICK", signal: props.signal, researchedAt: props.researchedAt, combination:Boolean(props.combinationCandidateId) });
  const product = representative?.candidate;
  const publish=useRef(props.onRepresentative);publish.current=props.onRepresentative;
  useEffect(()=>{publish.current?.(props.targetId,product?.candidateId);},[props.targetId,product?.candidateId]);
  const shown = product ? props.display?.(product) : undefined;
  // A saved product that has not been read yet has no name either; the row says so instead of showing a blank.
  const name = product ? product.title.trim() || l("Loading product details", "상품 정보를 불러오는 중") : props.status;
  const reason = representative ? reasonLabel(representative.reason, effectiveSort, props.criteria, l) : undefined;
  const caption = <span className="curation-result__caption">{props.title}{reason && <><span aria-hidden="true"> · </span><span className="curation-result__why" data-reason={representative?.reason}>{reason}</span></>}</span>;
  const sheetLabel = l("Open all {count} candidates of {title}", "{title} 후보 {count}개 펼치기", { count: props.count, title: props.title });
  // The next candidates and count open the list; the accessible name describes the action.
  const behind = ordered.filter(candidate => candidate.candidateId !== product?.candidateId).slice(0, 2);
  const more = Math.max(0, props.count - (product ? 1 : 0));
  const list = (className: string) => <Button type="button" emphasis="quiet" className={className} aria-haspopup="dialog" aria-label={sheetLabel} onClick={props.onOpenSheet}>
    {behind.length > 0 && <span className="curation-result__stack" aria-hidden="true">{behind.map(candidate => <span key={candidate.candidateId} className="curation-result__stack-item"><ProductMedia src={candidate.mediaUrl} alt="" /></span>)}</span>}
    {more > 0 && <span className="curation-result__list-count">{l("+{count}", "+{count}", { count: more })}</span>}
    <ChevronRight size={14} aria-hidden="true" />
  </Button>;

  if (props.layout === "row") {
    // Two places to click, two different things: the product opens its own details, the stack at the right edge opens the list.
    // The mall is its small logo right after the product's name; the row carries no score (owner 2026-09-23 —
    // the product's card and details keep it), and the space it had goes to the list on the right.
    const body = <>
      <span className="curation-result__thumb">{product ? <ProductMedia src={product.mediaUrl} alt="" /> : null}</span>
      <span className="curation-result__text">{caption}<span className="curation-result__name-line"><span className="curation-result__name">{name}</span>{product && <MallLogo source={product.source ?? "SHOPIFY"} className="curation-result__logo" />}</span></span>
      {product && <Price product={product} display={shown} />}
    </>;
    return <li className="curation-result curation-result--row" data-result-target={props.targetId} data-representative={product?.candidateId}>
      {product ? <Button type="button" emphasis="quiet" className="curation-result__row" aria-haspopup="dialog" aria-label={l("{title}: view {product}", "{title}: {product} 상세 보기", { title: props.title, product: name })} onClick={() => props.onOpenCandidate(product)}>{body}</Button>
        : <div className="curation-result__row curation-result__row--empty" role="status">{body}</div>}
      {list("curation-result__list")}
    </li>;
  }

  const strength = product ? comparativeStrength(product, props.cohort, props.criteria) : undefined;
  const why = product ? product.axisAssessment?.scores.find(score => score.axisId === strength?.axis.axisId)?.explanation || product.intentPoint : undefined;
  const cart = product ? props.cartAction(product) : undefined;
  const seller = product?.previewVariant?.sellerName;
  return <li className="curation-result curation-result--card" data-result-target={props.targetId} data-representative={product?.candidateId}>
    <h3 className="curation-result__head">{caption}</h3>
    {/* The whole card opens the product, not only its name and picture: a click anywhere on it that is not already
        a control's goes to the details. Keyboard and assistive technology use the title button — one tab stop, one name. */}
    {product ? <div className="curation-result__card" data-source={product.source ?? "SHOPIFY"}
      onClick={event => { if (!(event.target as Element).closest("button, a")) props.onOpenCandidate(product); }}>
      {/* The image opens the same details as the title beside it, so it stays a pointer shortcut: one tab stop and one name per action. */}
      <Button type="button" emphasis="quiet" className="curation-result__media" tabIndex={-1} aria-hidden="true" onClick={() => props.onOpenCandidate(product)}><ProductMedia src={product.mediaUrl} alt="" /></Button>
      <div className="curation-result__info">
        <Button type="button" emphasis="quiet" className="curation-result__title" aria-haspopup="dialog" onClick={() => props.onOpenCandidate(product)}>{name}</Button>
        <div className="curation-result__numbers"><span className="curation-result__where"><CandidateSourceBadge source={product.source ?? "SHOPIFY"} />{seller && seller !== sourceLabel(product.source ?? "SHOPIFY", l) && <span className="curation-result__seller">{seller}</span>}</span><Price product={product} display={shown} /><Score product={product} /></div>
        {why && <p className="curation-result__reason">{strength && <span className="candidate-comparison__axis">{l("#{axis}", "#{axis}", { axis: strength.axis.label })}</span>}{why}</p>}
      </div>
    </div> : <div className="curation-result__card is-empty" role="status">
      {/* The card's own shape while its product is being researched: the product arrives in the same place and size. */}
      <span className="curation-result__media-placeholder" aria-hidden="true" />
      <div className="curation-result__info"><p className="curation-result__status">{props.status}</p></div>
    </div>}
    <div className="curation-result__actions">
      {cart && <Button type="button" size="compact" emphasis={cart.emphasis ?? "primary"} busy={cart.busy} disabled={cart.disabled} onClick={cart.onAction}>{cart.label}</Button>}
      {product && <Button type="button" size="compact" emphasis="quiet" aria-haspopup="dialog" onClick={() => props.onOpenCandidate(product)}>{l("Details", "상세")}</Button>}
      {list("curation-result__list curation-result__more")}
    </div>
  </li>;
}

/** How much a research looked at. Every number is a Server fact; the popover keeps the sources that returned nothing. */
export function ComparisonFactsLine({ facts }: { facts: ComparisonFacts }) {
  const { l } = useLocale();
  const answered = facts.sources.filter(s => s.candidateCount > 0);
  const limit = 3;
  const names = [...answered.slice(0, limit).map(s => sourceLabel(s.source, l)), ...(answered.length > limit ? [l("+{count} more", "외 {count}곳", { count: answered.length - limit })] : [])].join(" · ");
  return <Popover><PopoverTrigger asChild>
    <Button type="button" emphasis="quiet" className="curation-result__facts" aria-label={l("How much was compared. Open the list of stores", "얼마나 비교했는지. 조사한 곳 목록 열기")}>
      <span>{[names, l("{observed} seen · {compared} compared", "{observed}개 확인 · {compared}개 비교", { observed: facts.observed, compared: facts.compared })].filter(Boolean).join(" — ")}</span>
      <ChevronDown size={13} aria-hidden="true" />
    </Button>
  </PopoverTrigger><PopoverContent align="start" collisionPadding={12} className="curation-result__facts-popover" aria-label={l("Where Vitlane looked", "조사한 곳")}>
    <strong>{l("Where Vitlane looked", "조사한 곳")}</strong>
    <ul>{facts.sources.map(s => <li key={s.source}><CandidateSourceBadge source={s.source} /><span>{s.candidateCount > 0 ? l("{count} products", "상품 {count}개", { count: s.candidateCount })
      : s.reasonCode?.includes("RATE_LIMIT") ? l("Skipped · call limit", "건너뜀 · 호출 한도") : s.status === "FAILED" ? l("Could not be reached", "응답 없음") : s.status === "SKIPPED" || s.status === "UNSUPPORTED" ? l("Not searched this time", "이번에는 찾지 않음") : l("No results", "결과 없음")}</span></li>)}</ul>
    <dl><div><dt>{l("Seen", "확인")}</dt><dd>{facts.observed}</dd></div><div><dt>{l("Already saved", "이미 있던 상품")}</dt><dd>{facts.duplicates}</dd></div><div><dt>{l("Left out", "제외")}</dt><dd>{facts.rejected}</dd></div><div><dt>{l("Added", "후보로 추가")}</dt><dd>{facts.admitted}</dd></div><div><dt>{l("Compared on the same criteria", "같은 기준으로 비교")}</dt><dd>{facts.compared}</dd></div>{facts.unevaluated > 0 && <div><dt>{l("Not scored yet", "아직 평가 전")}</dt><dd>{facts.unevaluated}</dd></div>}</dl>
    <small>{l("“Left out” counts products without a purchasable listing or rejected by the store’s API.", "“제외”는 구매 경로가 없거나 판매처 API가 거절한 상품 수예요.")}</small>
  </PopoverContent></Popover>;
}
