import { track } from "../../../shared/analytics/analytics";
import { useEffect, useRef, useState } from "react";
import { useLocale } from "../../../shared/i18n";
import { APIError, request } from "../../../shared/api/client";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { hydrateCatalogResearch, loadLiveVariantPage, type LiveCatalogProduct, type LiveVariantPage, type LiveVariantRow, type CatalogLikedVariantSnapshot } from "../research/infra/liveCatalogReviewApi";
import { amazonCandidateURL, readAmazonState, queueAmazonRead, type AmazonState } from "../research/infra/amazonApi";
import { variantInteractionKey, type VariantInteraction } from "../domain/candidatePresentation";
import { isAmazonAdmissionUnavailable } from "../domain/sourceProduct";
import { amazonPresentation, amazonVariant } from "../infra/candidatePresentation";
import { CurationCandidateCard } from "./CurationCandidateCard";
import { CandidateDetailDialog } from "./CandidateDetailDialog";
import { PurchaseCheckConfirmationDialog } from "./PurchaseCheckConfirmationDialog";
import { catalogLikedCandidatesChanged } from "../research/infra/catalogLikedCandidates";
import { externalPurchaseChanged, publishPurchaseFeedback, purchaseFeedbackUpdate } from "../research/infra/externalStateEvents";

// Source-specific I/O controller; all visible card and dialog structure is common.
export function AmazonCandidate({ product: initial, curationId, targetId, targetTitle, interactions, signals, configuredVariant, cardState, onStateStale, onInteraction, openRequest, onOpened, detailsOnly, onClosed }: {
  product: LiveCatalogProduct; curationId: string; targetId: string; targetTitle: string;
  interactions: Record<string, VariantInteraction>; configuredVariant?: LiveVariantRow;
  signals?: { pinned: boolean; liked: boolean; disliked: boolean };
  // The workspace read already carries this card's state; without it the card
  // falls back to reading its own, as it did before.
  cardState?: AmazonState;
  onStateStale?: () => void | Promise<void>;
  // A response names this product: a new value opens its details, as a click on the card would.
  openRequest?: number;
  /** The reader opened this product's details (it becomes what the response shows for its product group). */
  onOpened?: () => void;
  /** The conversation opens a product directly: only its details are drawn, never the card, and `onClosed` tells the owner to let go. */
  detailsOnly?: boolean;
  onClosed?: () => void;
  onInteraction: (key: string, value: VariantInteraction, snapshot: CatalogLikedVariantSnapshot, relationToken?: string) => Promise<void>;
}) {
  const { l } = useLocale();
  const [product, setProduct] = useState(initial);
  const [state, setState] = useState<AmazonState | undefined>(cardState);
  const stateRef = useRef<AmazonState | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(!initial.title);
  const [error, setError] = useState("");
  const [page, setPage] = useState<LiveVariantPage>();
  const [optionLoading, setOptionLoading] = useState(false);
  const [optionError, setOptionError] = useState(false);
  const [open, setOpen] = useState(false);
  const [purchaseConfirmationOpen, setPurchaseConfirmationOpen] = useState(false);
  const [draftId, setDraftId] = useState<string>();
  const [draftProduct, setDraftProduct] = useState<LiveCatalogProduct>();
  const [pageIndex, setPageIndex] = useState(0);
  const reactionPending = useRef(false);
  // One purchase-check command per attempt: a retry after a network failure replays the same key.
  const purchasePending = useRef<{ key: string; checked: boolean; version: number } | undefined>(undefined);
  const [reactionBusy, setReactionBusy] = useState(false);
  const [saving, setSaving] = useState(false);
  const lifetime = useRef<AbortController | null>(null);
  const selectionEpoch = useRef(0);
  const detailCache = useRef(new Map<string, LiveCatalogProduct>());
  const id = initial.candidateId;
  stateRef.current = state;
  const cardStateRef = useRef(cardState); cardStateRef.current = cardState;
  const staleRef = useRef(onStateStale); staleRef.current = onStateStale;
  // One workspace read answers every card, so a card that finds itself behind
  // asks the page for that read rather than for its own.
  function reloadState() {
    const stale = staleRef.current;
    if (stale) { void stale(); return; }
    void readAmazonState(curationId, id).then((value) => { if (!lifetime.current?.signal.aborted) setState(value); }).catch(() => undefined);
  }
  const savedId = state?.variantRef.asin;
  const savedRow = page?.rows.find((row) => row.variantId === savedId) || (configuredVariant?.variantId === savedId ? configuredVariant : undefined);
  const savedVariant = savedId ? amazonVariant(savedId, savedRow, product) : undefined;
  const selectedId = open ? draftId || savedId : savedId;
  const selectedRow = page?.rows.find((row) => row.variantId === selectedId) || (savedRow?.variantId === selectedId ? savedRow : undefined);
  const selected = selectedId ? amazonVariant(selectedId, selectedRow, draftProduct || product) : undefined;
  const selectedRecord = state?.purchaseFeedback?.records?.find((r) => r.variantRef?.asin === savedId);
  // The card signal covers every option of this Candidate, not only the saved one (ADR-0075).
  const purchased = Boolean(state?.purchaseFeedback?.records?.some((r) => r.candidateId === id && r.checked));
  const card = amazonPresentation(product, savedVariant);
  const detail = { ...amazonPresentation(draftProduct || product, selected), axisAssessment: product.axisAssessment };
  // Recommendation belongs to the Candidate, even when the selected ASIN changes.
  detail.title = card.title; detail.recommendation = card.recommendation; detail.features = card.features; detail.specifications = card.specifications;
  const key = selectedId ? variantInteractionKey(id, selectedId) : "";
  const interaction = interactions[key] ?? { pinned: false, sentiment: "NONE" as const };

  async function hydrate(signal?: AbortSignal) {
    setLoading(true);
    try {
      const result = await queueAmazonRead(() => hydrateCatalogResearch({ curationId, targetId, candidateId: id, source: "AMAZON", scope: "CANDIDATE", signal }), signal);
      if (signal?.aborted) return;
      const value = result.pools.flatMap((pool) => [...pool.products, ...pool.hiddenProducts]).find((p) => p.candidateId === id);
      if (value?.hydration?.status === "READY") { setProduct(value); setError(""); }
      else setError(isAmazonAdmissionUnavailable(value?.hydration?.reasonCode) ? "" : l("Amazon details are unavailable. You can retry or use the saved link.", "Amazon 상세 정보를 확인하지 못했습니다. 다시 시도하거나 저장된 링크를 이용해 주세요."));
    } catch { if (!signal?.aborted) setError(l("Amazon lookup failed. Try again later.", "Amazon 조회에 실패했습니다. 잠시 후 다시 시도해 주세요.")); }
    finally { if (!signal?.aborted) setLoading(false); }
  }
  useEffect(() => {
    const controller = new AbortController(); lifetime.current = controller;
    const read = (first = false) => void readAmazonState(curationId, id).then((value) => {
      if (controller.signal.aborted) return; setState(value);
      if (first && initial.variantObservation && initial.variantObservation.variantRef.asin !== value.variantRef.asin) void hydrate(controller.signal);
    }).catch(() => { if (!controller.signal.aborted) setError(l("Couldn't load your saved selection. Reload this page.", "저장된 선택을 불러오지 못했습니다. 화면을 새로고침해 주세요.")); });
    // The command response carries the new feedback, so a card only reloads its
    // saved selection when the event arrives without one. One workspace read
    // then serves every card, so a stale card asks the page instead of the
    // candidate endpoint; a page that cannot reload falls back to its own read.
    const changed = (event: Event) => {
      const update = purchaseFeedbackUpdate(event, curationId, stateRef.current?.purchaseFeedback);
      if (update.kind === "apply") setState((current) => (current ? { ...current, purchaseFeedback: update.feedback } : current));
      else if (update.kind === "reload") reloadState();
    };
    const workspace = cardStateRef.current;
    if (workspace) {
      if (initial.variantObservation && initial.variantObservation.variantRef.asin !== workspace.variantRef.asin) void hydrate(controller.signal);
    } else read(true);
    window.addEventListener(externalPurchaseChanged, changed);
    return () => { controller.abort(); selectionEpoch.current++; window.removeEventListener(externalPurchaseChanged, changed); };
  }, [curationId, id]);
  // A fresh workspace read is the authority, but a command this card just ran
  // can be newer than the read that was already in flight, so versions decide.
  useEffect(() => {
    const workspace = cardStateRef.current;
    if (!workspace) return;
    setState((current) => {
      if (!current) return workspace;
      const feedback = workspace.purchaseFeedback.version > current.purchaseFeedback.version ? workspace.purchaseFeedback : current.purchaseFeedback;
      const configured = workspace.configurationVersion > current.configurationVersion;
      if (feedback === current.purchaseFeedback && !configured) return current;
      return configured ? { ...workspace, purchaseFeedback: feedback } : { ...current, purchaseFeedback: feedback };
    });
  }, [cardState?.purchaseFeedback.version, cardState?.configurationVersion, cardState?.variantRef.asin]);
  useEffect(() => {
    if (initial.variantObservation && (!state || initial.variantObservation.variantRef.asin === state.variantRef.asin)) { setProduct(initial); setLoading(false); }
    else if (initial.hydration?.status === "FAILED" || initial.hydration?.status === "UNRESOLVED") {
      setLoading(false); setError(isAmazonAdmissionUnavailable(initial.hydration.reasonCode) ? "" : l("Amazon details are unavailable. You can retry or use the saved link.", "Amazon 상세 정보를 확인하지 못했습니다. 다시 시도하거나 저장된 링크를 이용해 주세요."));
    }
  }, [initial]);
  async function options(cursorToken?: string) {
    setOptionLoading(true); setOptionError(false); setError("");
    try {
      const result = await queueAmazonRead(() => loadLiveVariantPage({ curationId, candidateId: id, cursorToken }), lifetime.current?.signal);
      if (!lifetime.current?.signal.aborted) setPage((previous) => cursorToken && previous ? { ...result, rows: [...previous.rows, ...result.rows] } : result);
    } catch (error) { if (!lifetime.current?.signal.aborted) setOptionError(!(error instanceof APIError && isAmazonAdmissionUnavailable(error.reasonCode ?? error.code))); }
    finally { if (!lifetime.current?.signal.aborted) setOptionLoading(false); }
  }
  function openDetails() {
    setDraftId(savedId); setDraftProduct(product); setPageIndex(0); setOpen(true); void options();
  }
  const openRequested = useRef<number | undefined>(undefined);
  useEffect(() => { if (openRequest !== undefined && openRequested.current !== openRequest) { openRequested.current = openRequest; openDetails(); } });
  const opened = useRef(onOpened); opened.current = onOpened;
  useEffect(() => { if (open) opened.current?.(); }, [open]);
  function closeDetails() { selectionEpoch.current++; setOpen(false); setDraftId(undefined); setDraftProduct(undefined); onClosed?.(); }
  async function select(asin: string) {
    if (!page?.relationToken || !state) return;
    const epoch = ++selectionEpoch.current;
    setDraftId(asin); setDraftProduct(undefined); setError(""); setOptionLoading(false);
    if (asin === savedId && product.variantObservation?.variantRef.asin === asin) { setDraftProduct(product); return; }
    const cached = detailCache.current.get(asin);
    if (cached?.variantObservation && Date.parse(cached.variantObservation.refreshAfter) > Date.now()) { setDraftProduct(cached); return; }
    setOptionLoading(true);
    try {
      const result = await queueAmazonRead(() => request<{ product: LiveCatalogProduct }>(amazonCandidateURL(curationId, id, "resolve"), {
        method: "POST", body: JSON.stringify({ relationToken: page.relationToken, variantRef: { source: "AMAZON", marketplace: "US", asin } }),
      }), lifetime.current?.signal);
      if (epoch !== selectionEpoch.current || lifetime.current?.signal.aborted) return;
      detailCache.current.set(asin, result.product); setDraftProduct(result.product);
    } catch { if (epoch === selectionEpoch.current) setError(l("Couldn't load this option's details. You can retry the options.", "이 옵션의 상세 정보를 불러오지 못했습니다. 옵션을 다시 불러올 수 있습니다.")); }
    finally { if (epoch === selectionEpoch.current) setOptionLoading(false); }
  }
  async function save() {
    if (!page?.relationToken || !state || !selectedId || busy) return;
    setBusy(true); setSaving(true); setError("");
    try {
      await request(`/api/v1/curations/${encodeURIComponent(curationId)}/catalog-research/candidates/${encodeURIComponent(id)}/configuration`, { method: "PUT", body: JSON.stringify({ variantId: selectedId, selectedOptions: [], observedAt: page.observedAt, relationToken: page.relationToken, expectedVersion: state.configurationVersion }) });
      // The successful command is authoritative even if the following read fails.
      setState({ ...state, variantRef: { ...state.variantRef, asin: selectedId }, configurationVersion: state.configurationVersion + 1 });
      if (draftProduct) setProduct({ ...draftProduct, title: product.title, intentPoint: product.intentPoint, features: product.features, specifications: product.specifications });
      closeDetails();
    } catch { setError(l("Couldn't save this option. Reload the options and try again.", "옵션을 저장하지 못했습니다. 옵션 목록을 다시 불러온 후 시도해 주세요.")); reloadState(); }
    finally { setBusy(false); setSaving(false); }
  }
  async function react(patch: Partial<VariantInteraction>) {
    if (!selected || reactionPending.current) return;
    reactionPending.current = true; setReactionBusy(true); setError("");
    const next = { ...interaction, ...patch }; const price = selected.price;
    try {
      await onInteraction(key, next, { productTitle: card.title, variantTitle: selected.title || selected.attributes.map(({ value }) => value).join(" / ") || selected.id, productUrl: selected.productUrl,
        merchant: card.sellerName || "Amazon", targetTitle, priceMinor: price.kind === "OBSERVED" ? price.amountMinor : 0,
        priceUnknown: price.kind !== "OBSERVED", currency: price.kind === "OBSERVED" ? price.currency : "USD" }, page?.relationToken);
      if (next.sentiment !== interaction.sentiment) window.dispatchEvent(new Event(catalogLikedCandidatesChanged));
    } catch { setError(l("Couldn't save your reaction. Your previous pin and preference are unchanged.", "반응을 저장하지 못했습니다. 이전 핀과 선호도는 그대로 유지됩니다.")); }
    finally { reactionPending.current = false; setReactionBusy(false); }
  }
  async function check() {
    if (!state || busy) return;
    const operation = purchasePending.current ?? { key: randomUUID(), checked: !selectedRecord?.checked, version: selectedRecord?.version ?? 0 };
    purchasePending.current = operation; setBusy(true); setError("");
    // What the user sees now travels with the check so the account list can show it (ADR-0075).
    // It is the user's own record, never a catalog price; an unhydrated card sends none.
    const price = savedVariant?.price;
    const snapshot = operation.checked && card.title ? {
      productTitle: card.title,
      variantTitle: savedVariant ? savedVariant.title || savedVariant.attributes.map(({ value }) => value).join(" / ") || savedVariant.id : "",
      merchant: card.sellerName || "Amazon",
      priceMinor: price?.kind === "OBSERVED" ? price.amountMinor : 0,
      priceUnknown: price?.kind !== "OBSERVED",
      currency: price?.kind === "OBSERVED" ? price.currency : undefined,
    } : undefined;
    try {
      const feedback = await request<AmazonState["purchaseFeedback"]>(amazonCandidateURL(curationId, id, "purchase-check"), { method: "PUT", headers: { "Idempotency-Key": operation.key }, body: JSON.stringify({ variantRef: state.variantRef, checked: operation.checked, expectedVersion: operation.version, ...(snapshot ? { snapshot } : {}) }) });
      purchasePending.current = undefined; setState({ ...state, purchaseFeedback: feedback }); publishPurchaseFeedback(curationId, feedback);
    } catch (caught) {
      if (caught instanceof APIError && caught.status === 409) {
        // Another tab or a completed first attempt moved the version: reload the truth instead of retrying blind.
        purchasePending.current = undefined;
        setError(l("The purchase check changed elsewhere, so the current state was reloaded. Check it and try again.", "구매 체크 상태가 다른 곳에서 바뀌어 현재 상태를 다시 불러왔습니다. 확인 후 다시 시도해 주세요."));
        reloadState();
      } else {
        setError(l("Couldn't save the purchase check. Try again.", "구매 체크를 저장하지 못했습니다. 다시 시도해 주세요."));
      }
    }
    finally { setBusy(false); }
  }
  function requestPurchaseCheck() {
    if (selectedRecord?.checked) void check();
    else setPurchaseConfirmationOpen(true);
  }
  const visibleRows = page?.rows.slice(pageIndex * 5, pageIndex * 5 + 5) ?? (selectedRow ? [selectedRow] : []);
  const hasNext = Boolean(page && ((pageIndex + 1) * 5 < page.rows.length || page.pagination.hasNext));
  return <div className="catalog-ui-candidate-entry" data-candidate-id={id} hidden={detailsOnly || undefined}>
    {!detailsOnly && <CurationCandidateCard candidate={card} state={loading ? "loading" : "default"} signals={{ pinned: signals?.pinned ?? false, liked: signals?.liked ?? false, disliked: signals?.disliked ?? false, purchased }} onOpen={openDetails}
      primaryAction={{ label: l("External purchase", "외부 구매"), disabled: !savedVariant?.productUrl || busy, onAction: () => { if (savedVariant?.productUrl) { track({ name: "external_merchant_opened", source: "AMAZON" }); window.open(savedVariant.productUrl, "_blank", "noopener,noreferrer"); } } }}
      secondaryAction={{ label: selectedRecord?.checked ? l("Undo", "체크 취소") : l("Purchase check", "구매 체크"), disabled: !state || busy, onAction: requestPurchaseCheck }}
      statusMessage={error || (selectedRecord?.checked ? l("Marked by you. This is not an order confirmation.", "직접 구매했다고 표시했습니다. 주문 확인 내역은 아닙니다.") : undefined)} />}
    {open ? <CandidateDetailDialog candidate={detail} selected={selected} options={visibleRows.map((row) => amazonVariant(row.variantId, row, row.variantId === selectedId ? draftProduct || product : detailCache.current.get(row.variantId)))}
      loading={optionLoading} busy={busy || reactionBusy} optionError={optionError} error={error}
      notice={selectedId !== savedId ? l("Save this option to use it for an external purchase or purchase check.", "이 옵션으로 외부 구매하거나 구매 체크하려면 옵션을 저장해 주세요.") : undefined}
      partialOptions={page?.truncated || page?.relationStatus === "UNKNOWN"} pageNumber={page ? pageIndex + 1 : undefined}
      previousDisabled={pageIndex === 0} nextDisabled={!hasNext}
      onPrevious={pageIndex > 0 || hasNext ? () => setPageIndex((value) => Math.max(0, value - 1)) : undefined}
      onNext={pageIndex > 0 || hasNext ? () => { if (page && (pageIndex + 1) * 5 >= page.rows.length && page.pagination.hasNext) void options(page.pagination.nextCursor); setPageIndex((value) => value + 1); } : undefined}
      onReload={() => { detailCache.current.clear(); setPageIndex(0); void options(); }}
      interaction={interaction} reactionDisabled={!state || busy || reactionBusy || optionLoading} onReaction={(patch) => void react(patch)}
      onSelect={(asin) => void select(asin)} saved={Boolean(state?.configurationVersion && selectedId === savedId)} saving={saving}
      saveDisabled={busy || reactionBusy || optionLoading || !page?.relationToken} onSave={() => void save()}
      purchase={{ kind: "EXTERNAL", href: selected?.productUrl, checked: Boolean(selectedRecord?.checked), disabled: !state || busy || selectedId !== savedId, pending: busy && !saving, onCheck: requestPurchaseCheck }}
      onClose={closeDetails} /> : null}
    <PurchaseCheckConfirmationDialog open={purchaseConfirmationOpen} onOpenChange={setPurchaseConfirmationOpen} onConfirm={() => void check()} />
  </div>;
}
