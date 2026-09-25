import type { AxisAssessment } from "../domain/researchCriteria";
import { useEffect, useRef, useState } from "react";
import { LiveCatalogAPIError, loadLiveVariantPage, type LiveCartItem, type CatalogLikedVariantSnapshot, type LiveVariantPage, type LiveVariantRow, type LiveCatalogProviderMessage } from "../research/infra/liveCatalogReviewApi";
import { catalogLikedCandidatesChanged } from "../research/infra/catalogLikedCandidates";
import { useLocale, type Localize } from "../../../shared/i18n";
import { CandidateDetailDialog } from "./CandidateDetailDialog";
import { shopifyPresentation, shopifyVariant } from "../infra/candidatePresentation";
import { variantInteractionKey, type VariantInteraction } from "../domain/candidatePresentation";
export { variantInteractionKey, type VariantInteraction } from "../domain/candidatePresentation";

export type CatalogCandidateView = {
  curationId: string;
  targetId: string;
  candidateId: string;
  title: string;
  productUrl?: string;
  sellerDomain?: string;
  mediaUrl?: string;
  merchant: string;
  previewPriceMinor: number;
  currency: string;
  previewVariant?: LiveVariantRow;
  configuredVariantId?: string;
  preserveInitialVariantSelection?: boolean;
  axisAssessment?: AxisAssessment;
  intentPoint: string;
  features: string[];
  specifications: string[];
  searchPlatformMessages: LiveCatalogProviderMessage[];
  targetTitle: string;
  curationPath: string;
};

const emptyVariantInteraction: VariantInteraction = {
  pinned: false,
  sentiment: "NONE",
};

// Storefront fetch는 서버 pageSize(20) 단위 커서 페이지네이션을 유지하고,
// UI는 한 fetch 결과를 이 크기로 잘라 보여준다.
const VARIANT_UI_PAGE_SIZE = 5;

type Props = {
  candidate: CatalogCandidateView;
  country: string;
  currency: string;
  cartItems: LiveCartItem[];
  cartReady?: boolean;
  cartLoadFailed?: boolean;
  cartEditMode?: boolean;
  interactions: Record<string, VariantInteraction>;
  onAdd: (candidate: CatalogCandidateView, variant: LiveVariantRow, observedAt: string) => Promise<void>;
  onRetryCart?: () => void;
  onSaveOption: (candidate: CatalogCandidateView, variant: LiveVariantRow, observedAt: string) => Promise<void>;
  onClose: () => void;
  onInteraction: (
    key: string,
    value: VariantInteraction,
    likedSnapshot: CatalogLikedVariantSnapshot,
  ) => Promise<void>;
};

export function CatalogCandidateModal({
  candidate,
  cartItems,
  cartReady = true,
  cartLoadFailed = false,
  cartEditMode = false,
  interactions,
  onAdd,
  onRetryCart = () => undefined,
  onSaveOption,
  onClose,
  onInteraction,
}: Props) {
  const { l } = useLocale();
  const [pageNumber, setPageNumber] = useState(1);
  const [chunkIndex, setChunkIndex] = useState(0);
  const [page, setPage] = useState<LiveVariantPage>();
  const [selected, setSelected] = useState<LiveVariantRow | undefined>(
    candidate.previewVariant,
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [interactionError, setInteractionError] = useState<string>();
  const [candidateActionError, setCandidateActionError] = useState<string>();
  const [candidateActionPending, setCandidateActionPending] = useState<
    "CART" | "OPTION"
  >();
  const [optimisticInteractions, setOptimisticInteractions] = useState<
    Record<string, VariantInteraction>
  >({});
  const [cursorHistory, setCursorHistory] = useState<string[]>([""]);
  const pageCacheRef = useRef(new Map<string, LiveVariantPage>());
  const pageRequestsRef = useRef(new Map<string, Promise<LiveVariantPage>>());
  const selectionEstablishedRef = useRef(
    Boolean(candidate.preserveInitialVariantSelection || candidate.configuredVariantId),
  );
  const acknowledgedInteractionsRef = useRef(
    new Map<string, VariantInteraction>(),
  );
  const desiredInteractionsRef = useRef(
    new Map<string, VariantInteraction>(),
  );
  const interactionQueuesRef = useRef(new Map<string, Promise<void>>());
  useEffect(() => {
    if (!candidate.productUrl && !(candidate.sellerDomain && candidate.previewVariant?.variantId)) {
      setPage(undefined);
      setError(l("Options aren't available for this product.", "이 상품의 옵션을 불러올 수 없습니다."));
      return;
    }
    let active = true;
    const cursorToken = cursorHistory[pageNumber - 1] ?? "";
    const cached = pageCacheRef.current.get(cursorToken);
    if (cached) {
      setPage(cached);
      establishInitialVariantSelection(
        selectionEstablishedRef,
        cached.rows,
        setSelected,
      );
      setError(undefined);
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(undefined);
    let request = pageRequestsRef.current.get(cursorToken);
    if (!request) {
      request = loadLiveVariantPage({
        curationId: candidate.curationId,
        candidateId: candidate.candidateId,
        cursorToken: cursorToken || undefined,
      });
      pageRequestsRef.current.set(cursorToken, request);
    }
    void request
      .then((result) => {
        pageCacheRef.current.set(cursorToken, result);
        pageRequestsRef.current.delete(cursorToken);
        if (!active) return;
        setPage(result);
        establishInitialVariantSelection(
          selectionEstablishedRef,
          result.rows,
          setSelected,
        );
      })
      .catch((caught) => {
        pageRequestsRef.current.delete(cursorToken);
        if (!active) return;
        setError(
          caught instanceof LiveCatalogAPIError
            ? caught.fault.reasonCode
            : "LIVE_VARIANT_PAGE_UNAVAILABLE",
        );
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [candidate.candidateId, candidate.productUrl, candidate.sellerDomain, candidate.previewVariant?.variantId, cursorHistory, l, pageNumber]);

  // 최초 페이지 로드 시, 이미 선택된 Variant(설정 저장분·preview)가 속한
  // 5개 청크를 바로 보여준다. 이후 청크 이동은 사용자 조작만 따른다.
  const initialChunkRevealedRef = useRef(false);
  useEffect(() => {
    if (initialChunkRevealedRef.current || !page) return;
    initialChunkRevealedRef.current = true;
    const index = selected
      ? page.rows.findIndex(({ variantId }) => variantId === selected.variantId)
      : -1;
    if (index > 0) setChunkIndex(Math.floor(index / VARIANT_UI_PAGE_SIZE));
  }, [page, selected]);

  const rows = page?.rows.length ? page.rows : candidate.previewVariant ? [candidate.previewVariant] : [];
  const chunkCount = Math.max(1, Math.ceil(rows.length / VARIANT_UI_PAGE_SIZE));
  const activeChunk = Math.min(chunkIndex, chunkCount - 1);
  const visibleRows = rows.slice(
    activeChunk * VARIANT_UI_PAGE_SIZE,
    activeChunk * VARIANT_UI_PAGE_SIZE + VARIANT_UI_PAGE_SIZE,
  );
  const chunksPerFetch = Math.max(
    1,
    Math.ceil(
      (page?.pagination.pageSize ?? VARIANT_UI_PAGE_SIZE) / VARIANT_UI_PAGE_SIZE,
    ),
  );
  const uiPageNumber = (pageNumber - 1) * chunksPerFetch + activeChunk + 1;
  const interactionKey = selected
    ? variantInteractionKey(candidate.candidateId, selected.variantId)
    : "";
  const persistedInteraction = interactions[interactionKey] ?? emptyVariantInteraction;
  const interaction = optimisticInteractions[interactionKey] ?? persistedInteraction;
  useEffect(() => {
    if (!interactionKey || interactionQueuesRef.current.has(interactionKey)) return;
    acknowledgedInteractionsRef.current.set(interactionKey, persistedInteraction);
    desiredInteractionsRef.current.set(interactionKey, persistedInteraction);
  }, [interactionKey, persistedInteraction.pinned, persistedInteraction.sentiment]);
  const alreadyInCart = Boolean(
    selected &&
      cartItems.some(
        (item) =>
          item.candidateId === candidate.candidateId &&
          item.variantId === selected.variantId,
      ),
  );
  async function addSelectedVariant() {
    if (!selected || candidateActionPending) return;
    if (!selected.available) {
      setCandidateActionError("VARIANT_UNAVAILABLE");
      return;
    }
    setCandidateActionPending("CART");
    setCandidateActionError(undefined);
    try {
      await onAdd(
        candidate,
        selected,
        page?.observedAt ?? new Date().toISOString(),
      );
    } catch (caught) {
      setCandidateActionError(
        caught instanceof LiveCatalogAPIError
          ? caught.fault.reasonCode
          : "PHASE8_CART_SAVE_FAILED",
      );
    } finally {
      setCandidateActionPending(undefined);
    }
  }

  async function saveSelectedOption() {
    if (!selected || candidateActionPending) return;
    setCandidateActionPending("OPTION");
    setCandidateActionError(undefined);
    try {
      await onSaveOption(
        candidate,
        selected,
        page?.observedAt ?? new Date().toISOString(),
      );
    } catch (caught) {
      setCandidateActionError(
        caught instanceof LiveCatalogAPIError
          ? caught.fault.reasonCode
          : "PHASE8_CONFIGURATION_SAVE_FAILED",
      );
    } finally {
      setCandidateActionPending(undefined);
    }
  }

  function updateInteraction(patch: Partial<VariantInteraction>) {
    if (!interactionKey || !selected) return;
    const key = interactionKey;
    const selectedVariant = selected;
    const acknowledged =
      acknowledgedInteractionsRef.current.get(key) ?? persistedInteraction;
    const desired = desiredInteractionsRef.current.get(key) ?? acknowledged;
    const next = { ...desired, ...patch };
    acknowledgedInteractionsRef.current.set(key, acknowledged);
    desiredInteractionsRef.current.set(key, next);
    setOptimisticInteractions((current) => ({ ...current, [key]: next }));

    const previous = interactionQueuesRef.current.get(key) ?? Promise.resolve();
    let queued: Promise<void>;
    queued = previous
      .then(async () => {
        const acknowledgedBefore =
          acknowledgedInteractionsRef.current.get(key) ?? emptyVariantInteraction;
        try {
          await onInteraction(key, next, {
            productTitle: candidate.title,
            variantTitle: selectedVariant.title,
            productUrl: selectedVariant.productUrl || candidate.productUrl,
            merchant: candidate.merchant,
            priceMinor: selectedVariant.priceMinor,
            currency: selectedVariant.currency,
            targetTitle: candidate.targetTitle,
          });
          acknowledgedInteractionsRef.current.set(key, next);
          if (next.sentiment !== acknowledgedBefore.sentiment) {
            window.dispatchEvent(new Event(catalogLikedCandidatesChanged));
          }
          setInteractionError(undefined);
        } catch {
          const latestDesired = desiredInteractionsRef.current.get(key);
          if (sameInteraction(latestDesired, next)) {
            const rollback =
              acknowledgedInteractionsRef.current.get(key) ?? emptyVariantInteraction;
            desiredInteractionsRef.current.set(key, rollback);
            setOptimisticInteractions((current) => ({
              ...current,
              [key]: rollback,
            }));
          }
          setInteractionError(
            l("We couldn't save your reaction. Try again.", "반응을 저장하지 못했습니다. 다시 시도해 주세요."),
          );
        }
      })
      .finally(() => {
        if (interactionQueuesRef.current.get(key) !== queued) return;
        interactionQueuesRef.current.delete(key);
        const latestDesired = desiredInteractionsRef.current.get(key);
        const latestAcknowledged = acknowledgedInteractionsRef.current.get(key);
        if (!sameInteraction(latestDesired, latestAcknowledged)) return;
        setOptimisticInteractions((current) => {
          if (!(key in current)) return current;
          const nextState = { ...current };
          delete nextState[key];
          return nextState;
        });
      });
    interactionQueuesRef.current.set(key, queued);
  }

  const presentation = shopifyPresentation(candidate);
  presentation.observedAt = page?.observedAt;
  const showPages = page && (pageNumber > 1 || activeChunk > 0 || page.pagination.hasNext || rows.length > VARIANT_UI_PAGE_SIZE);
  return <CandidateDetailDialog candidate={presentation} selected={selected ? shopifyVariant(selected) : undefined}
    options={visibleRows.map(shopifyVariant)} loading={loading} busy={Boolean(candidateActionPending)} optionError={Boolean(error)}
    error={interactionError || (candidateActionError ? cartMutationErrorMessage(candidateActionError, candidate.title, l) : undefined) || (cartLoadFailed ? l("We couldn't confirm your cart. Reload it before making changes.", "장바구니를 확인하지 못했습니다. 변경하기 전에 다시 불러와 주세요.") : undefined)}
    pageNumber={page ? uiPageNumber : undefined} previousDisabled={pageNumber <= 1 && activeChunk <= 0}
    nextDisabled={!page?.pagination.hasNext && activeChunk >= chunkCount - 1}
    onPrevious={showPages ? () => {
      if (activeChunk > 0) { setChunkIndex(activeChunk - 1); return; }
      const token = cursorHistory[pageNumber - 2] ?? "";
      const count = pageCacheRef.current.get(token)?.rows.length ?? page!.pagination.pageSize;
      setChunkIndex(Math.max(0, Math.ceil(count / VARIANT_UI_PAGE_SIZE) - 1));
      setPageNumber((value) => Math.max(1, value - 1));
    } : undefined}
    onNext={showPages ? () => {
      if (activeChunk < chunkCount - 1) { setChunkIndex(activeChunk + 1); return; }
      setCursorHistory((current) => { const next = current.slice(0, pageNumber); next.push(page!.pagination.nextCursor ?? ""); return next; });
      setPageNumber((value) => value + 1); setChunkIndex(0);
    } : undefined}
    onReload={() => { pageCacheRef.current.clear(); setCursorHistory([""]); setPageNumber(1); setChunkIndex(0); }}
    interaction={interaction} reactionDisabled={!selected} onReaction={updateInteraction}
    onSelect={(id) => setSelected(rows.find((row) => row.variantId === id))}
    saved={Boolean(selected && selected.variantId === candidate.configuredVariantId)} saving={candidateActionPending === "OPTION"}
    saveDisabled={Boolean(candidateActionPending) || loading} onSave={() => void saveSelectedOption()}
    purchase={{ kind: "CART", pending: candidateActionPending === "CART",
      disabled: !selected || !selected.available || Boolean(candidateActionPending) || (!cartReady && !cartLoadFailed) || (loading && !cartLoadFailed),
      label: cartLoadFailed ? l("Reload cart", "장바구니 다시 불러오기") : !cartReady ? l("Loading cart", "장바구니 불러오는 중")
        : loading ? l("Loading product information", "상품 정보 불러오는 중") : selected && !selected.available ? l("Unavailable — choose another option", "구매 불가 — 다른 옵션 선택")
        : cartEditMode ? l("Apply option to cart", "장바구니 옵션 적용") : alreadyInCart ? l("Add one more", "한 개 더 담기") : l("Add to cart", "장바구니 담기"),
      onAction: () => { if (cartLoadFailed) onRetryCart(); else void addSelectedVariant(); },
    }} onClose={onClose} />;
}

function cartMutationErrorMessage(reasonCode: string, itemTitle: string, l: Localize) {
  if (reasonCode === "VARIANT_UNAVAILABLE") {
    return l(
      "{itemTitle} is currently unavailable and was not added to your cart. Choose another option.",
      "현재 구매할 수 없는 상품입니다: {itemTitle}. 장바구니에 담지 않았습니다. 다른 옵션을 선택해 주세요.",
      { itemTitle },
    );
  }
  if (reasonCode === "PHASE8_CART_VERSION_CONFLICT") {
    return l("The cart changed in another window. Review the latest contents and try again.", "장바구니가 다른 화면에서 변경되었습니다. 최신 내용을 확인하고 다시 시도해 주세요.");
  }
  if (reasonCode === "PHASE8_CART_NOT_READY") {
    return l("Check the cart state and try again.", "장바구니 상태를 확인한 뒤 다시 시도해 주세요.");
  }
  if (reasonCode.startsWith("PHASE8_CART_ITEM_INVALID")) {
    return l("The product information is not fully ready yet, so it could not be added. Try again shortly.", "상품 정보가 아직 완전히 준비되지 않아 담지 못했습니다. 잠시 후 다시 담아 주세요.");
  }
  return l("We couldn't save this product. Try again.", "상품을 저장하지 못했습니다. 다시 시도해 주세요.");
}

function sameInteraction(
  left: VariantInteraction | undefined,
  right: VariantInteraction | undefined,
) {
  return Boolean(
    left &&
      right &&
      left.pinned === right.pinned &&
      left.sentiment === right.sentiment,
  );
}

function establishInitialVariantSelection(
  established: { current: boolean },
  rows: LiveVariantRow[],
  select: (update: (current: LiveVariantRow | undefined) => LiveVariantRow | undefined) => void,
) {
  if (established.current) return;
  established.current = true;
  select((current) => {
    const currentRow = rows.find(({ variantId }) => variantId === current?.variantId);
    return (currentRow?.available ? currentRow : undefined) ??
      rows.find(({ available }) => available) ??
      currentRow ??
      rows[0] ??
      current;
  });
}
