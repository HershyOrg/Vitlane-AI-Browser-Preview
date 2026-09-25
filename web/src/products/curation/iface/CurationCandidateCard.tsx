import { useCandidateEvaluation } from "./ResearchComparison";
import { CandidateBudgetDelta } from "./BudgetViews";
import { formatCandidateMinor, useConvertedPrice } from "../research/app/useResearchCurrency";
import { BrandMark, CandidateCard } from "../../../shared/ui";
import type { CandidateCardAction, CandidateCardSignals, CandidateCardState } from "../../../shared/ui";
import { useLocale, type Localize } from "../../../shared/i18n";
import type { CandidatePresentation, CandidatePrice, CandidateSource, CandidateVariant } from "../domain/candidatePresentation";
import { sourceLabel } from "../domain/sourceLabels";
import { MallLogo } from "./MallLogo";
import "./candidate-presentation.css";

/** Where a product is sold: the mall's own icon and its name in plain text (owner 2026-09-23, no mall colours). */
export function CandidateSourceBadge({ source }: { source: CandidateSource }) {
  const { l } = useLocale();
  const name = sourceLabel(source, l);
  return <span className="candidate-source-badge" data-source={source}
    aria-label={l("Product source: {source}", "상품 출처: {source}", { source: name })}>
    <MallLogo source={source} decorative />
    <span className="candidate-source-badge__name">{name}</span>
  </span>;
}
export function candidatePriceLabel(price: CandidatePrice, l: Localize) {
  if (price.kind === "UNKNOWN") return l("Price unavailable", "가격 미확인");
  return price.kind === "OBSERVED" ? formatCandidateMinor(price.amountMinor, price.currency)
    : `${formatCandidateMinor(price.minimumMinor, price.currency)} – ${formatCandidateMinor(price.maximumMinor, price.currency)}`;
}
export function candidateOptionLabel(option: CandidateVariant | undefined, l: Localize, productTitle?: string) {
  const attributes = option?.attributes.map(({ value }) => value)
    .filter((value) => value.trim() && value.trim() !== productTitle?.trim()).join(" / ");
  const title = option?.title?.trim();
  return attributes || (title && title !== productTitle?.trim() ? title : undefined) || l("Option details unavailable", "옵션 미확인");
}
export function CurationCandidateCard({ candidate, state = "default", signals, onOpen, primaryAction, secondaryAction, statusMessage, evaluating = false }: {
  candidate: CandidatePresentation; state?: CandidateCardState; signals?: CandidateCardSignals; onOpen?: () => void;
  primaryAction?: CandidateCardAction; secondaryAction?: CandidateCardAction; statusMessage?: string;
  /** The Target's Round is still open: a card without a score is being scored, not left unscored. */
  evaluating?: boolean;
}) {
  const { l } = useLocale();
  const converted = useConvertedPrice(candidate.price);
  const evaluation = useCandidateEvaluation(candidate.id);
  const title = candidate.title || l("Recommended product", "추천 상품");
  const strengthReason = evaluation.assessment?.scores.find(score => score.axisId === evaluation.strength?.axis.axisId)?.explanation;
  return <div className="curation-candidate-card" data-source={candidate.source}>
    {evaluation.leader && <span className={`candidate-ranking-badge${evaluation.sort === "PICK" ? " is-pick" : ""}`}>{evaluation.sort === "COMBINATION" ? l("Combination fit", "조합 어울림") : evaluation.sort === "PRICE_ASC" ? l("Lowest price", "최저가") : evaluation.sort === "PRICE_DESC" ? l("Highest price", "최고가") : evaluation.sort === "PICK" ? <><BrandMark compact />{l("Pick", "Pick")}</> : l("#{axis} leader", "#{axis} 1위", { axis: evaluation.selectedAxis?.label ?? "" })}</span>}
    <CandidateCard density="compact" state={state} signals={signals}
      priceAccessory={<span className="candidate-comparison__score" aria-label={evaluation.assessment ? l("Overall score {score} out of 100", "총점 100점 중 {score}점", { score: evaluation.assessment.totalScore }) : undefined}>{evaluation.assessment ? <><span className="candidate-comparison__value">{evaluation.assessment.totalScore}</span><small>/100</small></> : evaluating ? l("Scoring", "평가 중") : l("Not scored", "미평가")}</span>}
      comparison={evaluation.strength ? <div className="candidate-comparison"><span className="candidate-comparison__axis">{l("#{axis}", "#{axis}", { axis: evaluation.strength.axis.label })}</span></div> : null} name={title}
      media={{ src: candidate.mediaUrl, alt: candidate.mediaAlt || title }}
      merchant={candidate.sellerName || l("Seller unconfirmed", "판매자 미확인")}
      sourceBadge={<CandidateSourceBadge source={candidate.source} />}
      price={[candidatePriceLabel(candidate.price, l), converted].filter(Boolean).join(" · ")}
      priceNote={<CandidateBudgetDelta price={candidate.price} />}
      recommendation={state === "unavailable" ? undefined : strengthReason || candidate.recommendation || undefined}
      optionSummary={candidate.priceScope === "PRODUCT" ? l("Product price · Options unconfirmed", "상품 가격 · 옵션 미확인") : candidate.selectedVariant ? candidateOptionLabel(candidate.selectedVariant, l, candidate.title) : l("Choose an option", "옵션 선택")}
      cardActionLabel={candidate.priceScope === "PRODUCT" ? l("View {title}", "{title} 상세 보기", { title }) : l("View options for {title}", "{title} 옵션 보기", { title })} onCardAction={onOpen}
      primaryAction={primaryAction && { ...primaryAction, emphasis: primaryAction.emphasis ?? (candidate.purchaseRoute === "VITLANE_CHECKOUT" && state !== "unavailable" ? "primary" : "secondary") }}
      secondaryAction={secondaryAction && { ...secondaryAction, emphasis: secondaryAction.emphasis ?? "tertiary" }}
      statusMessage={state === "unavailable" ? [candidate.recommendation, statusMessage].filter(Boolean).join(" ") : statusMessage} />
  </div>;
}
