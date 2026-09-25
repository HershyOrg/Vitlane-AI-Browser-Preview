import { useVisibleAnalytics } from "../../../shared/analytics/useVisibleAnalytics";
import { track, analyticsSource } from "../../../shared/analytics/analytics";
import { CandidateEvaluationDetails } from "./ResearchComparison";
import { CandidateBudgetDelta } from "./BudgetViews";
import { useConvertedPrice } from "../research/app/useResearchCurrency";
import { ChevronLeft, ChevronRight, ExternalLink, LoaderCircle, X } from "lucide-react";
import { useRef } from "react";
import { createPortal } from "react-dom";
import { useModalLayer } from "../app/useModalLayer";
import { Button, Disclosure, ProductMedia, sheetGrabberProps, useSwipeDismiss } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import type { CandidatePresentation, CandidateVariant, VariantInteraction } from "../domain/candidatePresentation";
import { CandidateSourceBadge, candidateOptionLabel, candidatePriceLabel } from "./CurationCandidateCard";
import { sourceLabel } from "../domain/sourceLabels";
import { VariantReactionButtons } from "./VariantReactionButtons";
import { CurationDismissibleNotice } from "./CurationDismissibleNotice";
import "./candidate-presentation.css";

const candidateSheetMedia = "(max-width: 52rem), (max-height: 35rem)";
// Everywhere else the details are a sheet on the right edge, the same kind of surface as the product group's list.
const candidateSideMedia = "(min-width: 52.0625rem) and (min-height: 35.0625rem)";

export type CandidatePurchaseAction =
  | { kind: "CART"; label: string; disabled: boolean; pending?: boolean; onAction: () => void }
  | { kind: "EXTERNAL"; href?: string; checked: boolean; disabled: boolean; pending?: boolean; onCheck: () => void };
export type CandidateDetailProps = {
  candidate: CandidatePresentation; selected?: CandidateVariant; options: CandidateVariant[];
  loading?: boolean; busy?: boolean; optionError?: boolean; error?: string; notice?: string; partialOptions?: boolean;
  pageNumber?: number; previousDisabled?: boolean; nextDisabled?: boolean;
  onPrevious?: () => void; onNext?: () => void; onReload?: () => void;
  interaction: VariantInteraction; reactionDisabled?: boolean; onReaction: (patch: Partial<VariantInteraction>) => void;
  productReactionAllowed?: boolean;
  onSelect: (id: string) => void; onSave: () => void; saveDisabled?: boolean; saved?: boolean; saving?: boolean;
  purchase: CandidatePurchaseAction; onClose: () => void;
};

export function CandidateDetailDialog(props: CandidateDetailProps) {
  const { candidate, selected, options, loading, busy, interaction, purchase } = props;
  const { l, locale } = useLocale();
  const converted = useConvertedPrice(selected?.price ?? candidate.price);
  const panel = useRef<HTMLElement>(null);
  const backdrop = useRef<HTMLDivElement>(null);
  useVisibleAnalytics(panel, { name: "candidate_viewed", source: analyticsSource(candidate.source) }, "candidate:" + candidate.id);
  // Same breakpoint as the bottom sheet presentation in candidate-presentation.css.
  const sheet = useSwipeDismiss(panel, { direction: "down", media: candidateSheetMedia, backdropRef: backdrop, onDismiss: props.onClose });
  const side = useSwipeDismiss(panel, { direction: "right", media: candidateSideMedia, backdropRef: backdrop, onDismiss: props.onClose });
  useModalLayer(panel, props.onClose);
  const title = candidate.title || l("Recommended product", "추천 상품");
  const selectedLabel = candidateOptionLabel(selected, l, title);
  return createPortal(<div ref={backdrop} className="catalog-ui-candidate-modal__backdrop candidate-detail-backdrop" role="presentation"
    onMouseDown={(event) => { if (event.target === event.currentTarget) props.onClose(); }}>
    <section ref={panel} className="catalog-ui-candidate-modal candidate-detail" role="dialog" aria-modal="true"
      aria-labelledby="phase8-candidate-modal-title" data-source={candidate.source}>
      {sheet ? <div {...sheetGrabberProps} /> : null}
      {side ? <div className="curation-side-grabber" data-swipe-dismiss="handle" aria-hidden="true" /> : null}
      <div className="candidate-detail__content">
      <header className="catalog-ui-candidate-modal__header" data-swipe-dismiss="handle">
        <div className="catalog-ui-candidate-modal__media"><ProductMedia src={selected?.mediaUrl || candidate.mediaUrl} alt={title} /></div>
        <div className="catalog-ui-candidate-modal__identity">
          <h2 id="phase8-candidate-modal-title">{title}</h2>
          <CandidateSourceBadge source={candidate.source} />
          <strong>{[candidatePriceLabel(selected?.price ?? candidate.price, l), converted].filter(Boolean).join(" · ")}<CandidateBudgetDelta price={selected?.price ?? candidate.price} /></strong>
          <p>{selectedLabel}</p>
          {selected?.productUrl || candidate.productUrl ? <a className="catalog-ui-candidate-modal__product-url" onClick={() => track({ name: "external_merchant_opened", source: analyticsSource(candidate.source) })} href={selected?.productUrl || candidate.productUrl} target="_blank" rel="noopener noreferrer"
            aria-label={l("Open the {title} product page in a new window", "{title} 상품 페이지 열기, 새 창", { title })}>
            <ExternalLink size={14} aria-hidden="true" />{l("Product page", "상품 페이지")}</a> : null}
        </div>
        <Button type="button" emphasis="quiet" aria-label={l("Close product details", "상품 상세 닫기")} onClick={props.onClose}><X aria-hidden="true" /></Button>
      </header>
      <div className="catalog-ui-candidate-modal__body">
        {candidate.priceScope !== "PRODUCT" && <section className="catalog-ui-variant-browser" aria-labelledby="phase8-variant-title">
          <header><h3 id="phase8-variant-title">{l("Options", "옵션")}</h3>
            <strong>{props.pageNumber ? l("Page {number}", "Page {number}", { number: props.pageNumber }) : l("Preview", "Preview")}</strong></header>
          {loading ? <div className="catalog-ui-variant-browser__state" role="status"><LoaderCircle className="catalog-ui-spin" aria-hidden="true" />{l("Loading product options.", "상품 옵션을 불러오고 있습니다.")}</div> : null}
          {props.optionError ? <CurationDismissibleNotice noticeId={`${candidate.id}:options`} tone="warning">{l("We couldn't refresh the options. The saved option may be out of date.", "옵션을 새로 불러오지 못했습니다. 저장된 옵션이 최신 정보가 아닐 수 있습니다.")}</CurationDismissibleNotice> : null}
          {!loading && !options.length ? <p>{l("There are no selectable options.", "선택 가능한 옵션이 없습니다.")}</p> : null}
          <div className="catalog-ui-variant-browser__rows" role="radiogroup" aria-label={l("Select option", "옵션 선택")}>
            {options.map((option) => <Button key={option.id} type="button" emphasis="quiet" role="radio" aria-checked={selected?.id === option.id}
              disabled={busy || !option.selectable} className="catalog-ui-variant-row" onClick={() => props.onSelect(option.id)}
              onKeyDown={(event) => {
                if (!["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight"].includes(event.key)) return;
                event.preventDefault(); const enabled = options.filter((item) => item.selectable);
                const direction = ["ArrowUp", "ArrowLeft"].includes(event.key) ? -1 : 1;
                const next = enabled[(enabled.findIndex((item) => item.id === option.id) + direction + enabled.length) % enabled.length];
                if (next) { props.onSelect(next.id); const buttons = Array.from(event.currentTarget.parentElement!.querySelectorAll<HTMLButtonElement>('[role="radio"]'));
                  buttons[options.findIndex((item) => item.id === next.id)]?.focus(); }
              }}>
              <span><strong>{candidateOptionLabel(option, l, title)}</strong></span>
              <span><strong>{candidatePriceLabel(option.price, l)}<CandidateBudgetDelta price={option.price} /></strong><small>{option.availability === "AVAILABLE" ? l("Available now", "현재 구매 가능") : option.availability === "UNAVAILABLE" ? l("Currently unavailable", "현재 구매 불가") : l("Availability unconfirmed", "구매 가능 여부 미확인")}</small></span>
            </Button>)}
          </div>
          {props.partialOptions ? <p className="candidate-detail__note">{l("Additional options may be available on the product page.", "상품 페이지에 추가 옵션이 있을 수 있습니다.")}</p> : null}
          {props.onPrevious || props.onNext ? <nav className="catalog-ui-variant-browser__pagination" aria-label={l("Option pages", "옵션 페이지")}>
            <Button emphasis="quiet" disabled={loading || props.previousDisabled} onClick={props.onPrevious}><ChevronLeft size={16} aria-hidden="true" />{l("Previous", "이전")}</Button>
            <span>{l("Up to {count} options per page", "페이지당 최대 옵션 {count}개", { count: 5 })}</span>
            <Button emphasis="quiet" disabled={loading || props.nextDisabled} onClick={props.onNext}>{l("Next", "다음")}<ChevronRight size={16} aria-hidden="true" /></Button>
          </nav> : null}
        </section>}
        {(candidate.priceScope !== "PRODUCT" || props.productReactionAllowed) && <VariantReactionButtons value={interaction} subject={candidate.priceScope === "PRODUCT" ? "PRODUCT" : "VARIANT"} disabled={props.reactionDisabled || (candidate.priceScope !== "PRODUCT" && !selected)} onChange={props.onReaction} />}
        {props.error ? <CurationDismissibleNotice noticeId={JSON.stringify([candidate.id, selected?.id, props.error])} tone="danger">{props.error}</CurationDismissibleNotice> : null}
        {props.notice ? <p className="candidate-detail__note" role="status">{props.notice}</p> : null}
        <section className="catalog-ui-candidate-evidence" aria-label={l("Why this product", "이 상품을 추천한 이유")}>
          <CandidateDisclosures candidate={candidate} />
          <CandidateEvaluationDetails assessment={candidate.axisAssessment} />
          <Disclosure className="candidate-detail__section" defaultOpen summary={l("Research notes", "조사 근거")}><p>{candidate.recommendation || l("Recommendation details unavailable", "추천 근거 미확인")}</p></Disclosure>
          <Disclosure className="candidate-detail__section" defaultOpen summary={l("Features", "특징")}><ul>{(candidate.features.length ? candidate.features : [l("No additional key feature information", "추가 핵심 기능 정보 없음")]).map((value) => <li key={value}>{value}</li>)}</ul></Disclosure>
          <Disclosure className="candidate-detail__section" defaultOpen summary={l("Specifications", "사양")}><ul>{(candidate.specifications.length ? candidate.specifications : [l("No additional technical specifications", "추가 기술 사양 정보 없음")]).map((value) => <li key={value}>{value}</li>)}</ul></Disclosure>
          <Disclosure className="candidate-detail__section" defaultOpen summary={l("Product information", "상품 정보")}><dl className="candidate-detail__facts">
            {candidate.provenance && <><dt>{l("Data provider", "API 제공자")}</dt><dd>{candidate.provenance.apiProvider} · {candidate.provenance.apiProduct}</dd><dt>{l("Discovery channel", "발견 경로")}</dt><dd>{candidate.provenance.discoveryChannel}</dd>{candidate.provenance.detailApiProvider && <><dt>{l("Product details provider", "상품 상세 제공자")}</dt><dd>{candidate.provenance.detailApiProvider} · {candidate.provenance.detailApiProduct}</dd></>}<dt>{l("Original product ID", "원본 상품 ID")}</dt><dd>{candidate.originalProductId}</dd></>}
            <dt>{l("Seller", "판매자")}</dt><dd>{candidate.sellerName || l("Seller unconfirmed", "판매자 미확인")}</dd>
            <dt>{l("Observed at", "확인 시각")}</dt><dd>{candidate.observedAt ? new Date(candidate.observedAt).toLocaleString(locale) : l("Unknown", "미확인")}</dd>
          </dl>{selected?.attributes.length ? <dl className="candidate-detail__facts">{selected.attributes.map((attr, index) => <div key={`${attr.name}:${index}`}><dt>{attr.name}</dt><dd>{attr.value}</dd></div>)}</dl> : null}</Disclosure>
        </section>
      </div>
      </div>
      <footer className="catalog-ui-candidate-modal__footer">
        <div>{purchase.kind === "CART" ? <><span>{l("Items in your cart are not reserved.", "장바구니 상품은 예약되지 않습니다.")}</span><strong>{l("We’ll confirm price and availability before checkout", "가격과 구매 가능 여부는 결제 전에 확인합니다")}</strong></>
          : <><span>{l("Confirm the final price and delivery on the product page.", "최종 가격과 배송은 상품 페이지에서 확인해 주세요.")}</span><strong>{l("Purchase checks are your own records, not order confirmations.", "구매 체크는 직접 남긴 기록이며 주문 확인이 아닙니다.")}</strong></>}</div>
        <div className="catalog-ui-candidate-modal__footer-actions" data-purchase-kind={purchase.kind}>
          {candidate.priceScope !== "PRODUCT" && <Button className="candidate-detail__save-action" emphasis="secondary" busy={props.saving} disabled={!selected || props.saveDisabled || props.saved} onClick={props.onSave}>{props.saved ? l("Option saved", "옵션 저장됨") : l("Save option", "옵션 저장")}</Button>}
          {purchase.kind === "CART" ? <Button className="candidate-detail__cart-action" emphasis="primary" busy={purchase.pending} disabled={purchase.disabled} onClick={purchase.onAction}>{purchase.label}</Button>
            : <div className="candidate-detail__external-actions"><Button emphasis="tertiary" busy={purchase.pending} disabled={purchase.disabled} onClick={purchase.onCheck}>{purchase.checked ? l("Undo", "체크 취소") : l("Purchase check", "구매 체크")}</Button>
              <Button emphasis="secondary" disabled={!purchase.href || (candidate.priceScope !== "PRODUCT" && purchase.disabled)} onClick={() => { if (purchase.href) { track({ name: "external_merchant_opened", source: analyticsSource(candidate.source) }); window.open(purchase.href, "_blank", "noopener,noreferrer"); } }}>{l("External purchase", "외부 구매")}</Button></div>}
        </div>
      </footer>
    </section>
  </div>, document.body);
}

function CandidateDisclosures({ candidate }: { candidate: CandidatePresentation }) {
  const { l } = useLocale();
  if (!candidate.disclosures.length) return null;
  const source = sourceLabel(candidate.source, l);
  return <section className="catalog-ui-candidate-search-platform" aria-label={l("Required {source} information for {subject}", "{subject} 관련 {source} 필수 안내", { source, subject: candidate.title })}>
    <header><h3>{l("{source} information", "{source} 안내", { source })}</h3><CandidateSourceBadge source={candidate.source} /></header>
    <ul aria-label={l("Required {source} display information", "{source} 필수 표시 정보", { source })}>{candidate.disclosures.map((message, index) => {
      const image = safeURL(message.imageUrl), link = safeURL(message.url);
      return <li key={index} role="note" data-content-type={message.contentType || "plain"} data-message-code={message.code} data-provider-path={message.path} data-presentation={message.presentation} data-severity={message.severity}>
        {image ? <img src={image} alt={l("{source} information related to {subject}", "{subject} 관련 {source} 안내 이미지", { source, subject: candidate.title })} loading="lazy" referrerPolicy="no-referrer" /> : null}
        <p>{message.content}</p>{link ? <a href={link} target="_blank" rel="nofollow noopener noreferrer" aria-label={l("View detailed {source} information for {subject} in a new window", "{subject} {source} 안내 자세히 보기, 새 창", { source, subject: candidate.title })}>{l("View details", "안내 자세히 보기")}<ExternalLink size={13} aria-hidden="true" /></a> : null}
      </li>;
    })}</ul>
  </section>;
}
function safeURL(value?: string) { try { const url = new URL(value || ""); return url.protocol === "https:" && !url.username && !url.password ? url.href : undefined; } catch { return undefined; } }
