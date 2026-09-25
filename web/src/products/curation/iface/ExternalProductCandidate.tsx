import { track, analyticsSource } from "../../../shared/analytics/analytics";
import { useEffect, useRef, useState } from "react";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { APIError, request } from "../../../shared/api/client";
import { Button } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
import type { PurchaseFeedback } from "../research/infra/amazonApi";
import type { ExternalProductCardState } from "../research/infra/externalCardState";
import type { CandidatePresentation } from "../domain/candidatePresentation";
import { observedPrice } from "../infra/candidatePresentation";
import { CurationCandidateCard } from "./CurationCandidateCard";
import { CandidateDetailDialog } from "./CandidateDetailDialog";
import { PurchaseCheckConfirmationDialog } from "./PurchaseCheckConfirmationDialog";
import type { VariantInteraction } from "../domain/candidatePresentation";
import { catalogLikedCandidatesChanged } from "../research/infra/catalogLikedCandidates";
import {
  externalPurchaseChanged,
  productReactionsChanged,
  publishProductReaction,
  publishPurchaseFeedback,
  purchaseFeedbackUpdate,
  reactionEventIsForAnotherCard,
} from "../research/infra/externalStateEvents";

type ProductReaction = VariantInteraction & { version: number };
type ExternalProductState = { purchaseFeedback: PurchaseFeedback; reactionAllowed?: boolean; reaction?: ProductReaction };

export function ExternalProductCandidate({ product, curationId, cardState, onStateStale, openRequest, onOpened, detailsOnly, onClosed }: {
  product: LiveCatalogProduct; curationId: string;
  // The workspace read already carries this card's state; without it the card
  // falls back to reading its own, as it did before.
  cardState?: ExternalProductCardState;
  onStateStale?: () => void | Promise<void>;
  // A response names this product: a new value opens its details, as a click on the card would.
  openRequest?: number;
  /** The reader opened this product's details (it becomes what the response shows for its product group). */
  onOpened?: () => void;
  /** The conversation opens a product directly: only its details are drawn, never the card, and `onClosed` tells the owner to let go. */
  detailsOnly?: boolean;
  onClosed?: () => void;
}) {
  const { l } = useLocale();
  const [feedback, setFeedback] = useState<PurchaseFeedback | undefined>(cardState?.purchaseFeedback);
  const [open, setOpen] = useState(false);
  const openRequested = useRef<number | undefined>(undefined);
  useEffect(() => { if (openRequest !== undefined && openRequested.current !== openRequest) { openRequested.current = openRequest; setOpen(true); } }, [openRequest]);
  const opened = useRef(onOpened); opened.current = onOpened;
  useEffect(() => { if (open) opened.current?.(); }, [open]);
  const closeDetails = () => { setOpen(false); onClosed?.(); };
  const [purchaseConfirmationOpen, setPurchaseConfirmationOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const [reaction, setReaction] = useState<ProductReaction | undefined>(cardState?.reaction);
  const [reactionAllowed, setReactionAllowed] = useState(cardState?.reactionAllowed === true);
  const [reactionError, setReactionError] = useState(false);
  const [savingReaction, setSavingReaction] = useState(false);
  const reactionPending = useRef(false);
  const pending = useRef<{ key: string; checked: boolean; version: number } | undefined>(undefined);
  const feedbackRef = useRef<PurchaseFeedback | undefined>(undefined);
  feedbackRef.current = feedback;
  const cardStateRef = useRef(cardState); cardStateRef.current = cardState;
  const staleRef = useRef(onStateStale); staleRef.current = onStateStale;
  const loadRef = useRef<() => void>(() => undefined);
  const observation = product.externalObservation;
  const path = `/api/v1/curations/${encodeURIComponent(curationId)}/catalog-research/candidates/${encodeURIComponent(product.candidateId)}/external-product`;
  useEffect(() => {
    let active = true;
    let generation = 0;
    const read = () => { const current = ++generation; return void request<ExternalProductState>(path).then((result) => { if (active && current === generation) { setFeedback(result.purchaseFeedback); setReaction(previous => previous && result.reaction && previous.version > result.reaction.version ? previous : result.reaction); setReactionAllowed(result.reactionAllowed === true); setError(false); } }).catch(() => { if (active && current === generation) { setError(true); setReactionAllowed(false); } }); };
    // One workspace read serves every card, so a stale card asks the page for
    // that read; a page that cannot reload falls back to this card's own read.
    const load = () => { const stale = staleRef.current; if (stale) void stale(); else read(); };
    loadRef.current = load;
    const workspace = cardStateRef.current;
    if (workspace) { setFeedback(workspace.purchaseFeedback); setReaction(workspace.reaction); setReactionAllowed(workspace.reactionAllowed); setError(false); }
    else { setReaction(undefined); setReactionAllowed(false); read(); }
    // A command that already returned the new value travels with its event, so
    // the other cards apply it instead of asking the server again.
    const purchased = (event: Event) => {
      const update = purchaseFeedbackUpdate(event, curationId, feedbackRef.current);
      if (update.kind === "apply") setFeedback(update.feedback);
      else if (update.kind === "reload") load();
    };
    const reacted = (event: Event) => { if (!reactionEventIsForAnotherCard(event, curationId, product.candidateId)) load(); };
    window.addEventListener(externalPurchaseChanged, purchased); window.addEventListener(productReactionsChanged, reacted);
    return () => { active = false; window.removeEventListener(externalPurchaseChanged, purchased); window.removeEventListener(productReactionsChanged, reacted); };
  }, [path]);
  // A fresh workspace read is the authority, but a command this card just ran
  // can be newer than the read that was already in flight, so versions decide.
  // Arriving state also answers a read that had failed, so it clears the error.
  useEffect(() => {
    const workspace = cardStateRef.current;
    if (!workspace) return;
    setError(false);
    setFeedback((current) => (current && current.version >= workspace.purchaseFeedback.version ? current : workspace.purchaseFeedback));
    setReaction((current) => (current && current.version >= workspace.reaction.version ? current : workspace.reaction));
    setReactionAllowed(workspace.reactionAllowed);
  }, [cardState?.purchaseFeedback.version, cardState?.reaction.version, cardState?.reaction.pinned, cardState?.reaction.sentiment, cardState?.reactionAllowed]);
  if (!observation) return null;
  const record = feedback?.records.find((r) => r.productRef?.source === observation.productRef.source && r.productRef.productId === observation.productRef.productId);
  const canReact = reactionAllowed && !product.previewVariant && !product.variantObservation && !product.locator?.variantId;
  async function react(patch: Partial<VariantInteraction>) {
    if (!canReact || !reaction || !observation || reactionPending.current) return;
    reactionPending.current = true; setSavingReaction(true); setReactionError(false);
    try {
      const result = await request<{ reaction: ProductReaction }>(`${path}/reaction`, { method: "PUT", body: JSON.stringify({ schemaVersion: "vitlane.product-reaction.v1", productRef: observation.productRef, pinned: patch.pinned ?? reaction.pinned, sentiment: patch.sentiment ?? reaction.sentiment, expectedVersion: reaction.version }) });
      setReaction(result.reaction); window.dispatchEvent(new Event(catalogLikedCandidatesChanged)); publishProductReaction(curationId, product.candidateId);
    } catch {
      setReactionError(true); loadRef.current();
    } finally { reactionPending.current = false; setSavingReaction(false); }
  }
  const candidate: CandidatePresentation = {
    id: product.candidateId, source: observation.productRef.source, title: observation.title,
    price: observation.price.kind === "OBSERVED" ? observedPrice(observation.price.amountMinor, observation.price.currency) : { kind: "UNKNOWN" },
    priceScope: "PRODUCT", productUrl: observation.productUrl, mediaUrl: observation.imageUrl,
    sellerName: observation.seller.kind === "KNOWN" ? observation.seller.name : undefined,
    observedAt: observation.observedAt, purchaseRoute: "EXTERNAL", recommendation: product.intentPoint,
    axisAssessment: product.axisAssessment, features: product.features, specifications: product.specifications, disclosures: [],
    provenance: observation.provenance, originalProductId: observation.productRef.productId,
  };
  async function check() {
    if (!observation || !feedback || busy) return;
    const operation = pending.current ?? { key: randomUUID(), checked: !record?.checked, version: record?.version ?? 0 };
    pending.current = operation; setBusy(true); setError(false);
    try {
      const result = await request<PurchaseFeedback>(`${path}/purchase-check`, { method: "PUT", headers: { "Idempotency-Key": operation.key }, body: JSON.stringify({ schemaVersion: "vitlane.external-product-purchase.v1", productRef: observation.productRef, checked: operation.checked, expectedVersion: operation.version }) });
      pending.current = undefined; setFeedback(result); publishPurchaseFeedback(curationId, result);
    } catch (caught) {
      setError(true);
      // A conflict means someone else moved the record, so every card reloads.
      if (caught instanceof APIError && caught.status === 409) { pending.current = undefined; publishPurchaseFeedback(curationId); }
    } finally { setBusy(false); }
  }
  function requestPurchaseCheck() {
    if (record?.checked) void check();
    else setPurchaseConfirmationOpen(true);
  }
  const notice = l("Product-level observation; options are unconfirmed. Check the current terms on the seller’s site.", "상품 수준의 관찰이며 옵션은 미확인입니다. 현재 조건은 판매 사이트에서 확인해 주세요.");
  return <>
    {error && !detailsOnly && <Button emphasis="quiet" onClick={() => loadRef.current()}>{l("Reload purchase record", "구매 기록 다시 불러오기")}</Button>}
    {!detailsOnly && <CurationCandidateCard candidate={candidate} signals={{ pinned: reaction?.pinned ?? false, liked: reaction?.sentiment === "LIKE", disliked: reaction?.sentiment === "DISLIKE", purchased: Boolean(record?.checked) }} onOpen={() => setOpen(true)}
      statusMessage={error ? l("Purchase record unavailable. Open details and try again.", "구매 기록을 확인하지 못했습니다. 상세에서 다시 시도해 주세요.") : record?.checked ? l("Marked by you. This is not an order confirmation.", "직접 구매했다고 표시했습니다. 주문 확인 내역은 아닙니다.") : undefined}
      primaryAction={{ label: l("External purchase", "외부 구매"), onAction: () => { track({ name: "external_merchant_opened", source: analyticsSource(observation.productRef.source) }); window.open(observation.productUrl, "_blank", "noopener,noreferrer"); } }}
      secondaryAction={{ label: record?.checked ? l("Undo", "체크 취소") : l("Purchase check", "구매 체크"), disabled: !feedback || busy, onAction: requestPurchaseCheck }} />}
    {open && <CandidateDetailDialog candidate={candidate} options={[]} interaction={reaction ?? { pinned: false, sentiment: "NONE" }} productReactionAllowed={canReact} reactionDisabled={!reaction || savingReaction} onReaction={patch => void react(patch)} onSelect={() => {}} onSave={() => {}} onClose={closeDetails}
      notice={notice} error={reactionError ? l("Your reaction was not saved. Review the current state and try again.", "반응을 저장하지 못했습니다. 현재 상태를 확인한 뒤 다시 시도해 주세요.") : error ? l("We could not confirm the purchase record. Try again.", "구매 기록을 확인하지 못했습니다. 다시 시도해 주세요.") : undefined}
      purchase={{ kind: "EXTERNAL", href: observation.productUrl, checked: Boolean(record?.checked), disabled: !feedback || busy, pending: busy, onCheck: requestPurchaseCheck }} />}
    <PurchaseCheckConfirmationDialog open={purchaseConfirmationOpen} onOpenChange={setPurchaseConfirmationOpen} onConfirm={() => void check()} />
  </>;
}
