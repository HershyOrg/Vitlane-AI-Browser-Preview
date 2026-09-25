import { CombinationCartAction } from "./CombinationCartAction";
import { threadActive, threadResponse } from "../domain/thread";
import { ProductVerticalBadge } from "./ProductVerticalBadge";
import { useConversationActivity, conversationError } from "./CurationFollowUps";
import { isKoreanExternalSource } from "../domain/sourceLabels";
import { executeConversationRequest } from "../infra/curationApi";
import { useCurationThreads } from "../app/useThreads";
import { useConversationResults } from "../app/useConversationResults";
import { comparisonFacts, liveResultHolders, turnResultGroups } from "../domain/conversationResults";
import { ComparisonFactsLine, ResultTarget } from "./CurationResults";
import { CurationThreadProgress } from "./CurationThread";
import { CriteriaEditor, TargetComparison, useTargetCriteria } from "./ResearchComparison";
import { BudgetProvider, BudgetTargetContext } from "../app/useBudget";
import { BudgetBar } from "./BudgetBar";
import { CartBudgetView, TargetBudgetLabel } from "./BudgetViews";
import { CurationComposer, focusLabel, type Focus } from "./CurationComposer";
import { CurationComposerDock } from "./CurationComposerDock";
import { CandidateGrid } from "./CandidateGrid";
import { TargetSheet } from "./TargetSheet";
import { useTargetViews } from "../app/useTargetViews";
import { useModalLayer } from "../app/useModalLayer";
import { CurationDismissibleNotice, CurationSourceNotices } from "./CurationDismissibleNotice";
import { ResearchCurrencyProvider } from "../research/app/useResearchCurrency";
import { ResearchSettings } from "../research/iface/ResearchSettings";
import type { CatalogLikedVariantSnapshot } from "../research/infra/liveCatalogReviewApi";
import { CurationCandidateCard } from "./CurationCandidateCard";
import { productPrice } from "../domain/candidatePresentation";
import { shopifyPresentation } from "../infra/candidatePresentation";
import { ExternalProductCandidate } from "./ExternalProductCandidate";
import { AmazonCandidate } from "./AmazonCandidate";
import { amazonCardState, externalCardState, externalProductCardState } from "../research/infra/externalCardState";
import { queueAmazonRead } from "../research/infra/amazonApi";
import {
  Check,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  Eye,
  EyeOff,
  LoaderCircle,
  Search,
  ShoppingBag,
  X,
} from "lucide-react";
import { createPortal } from "react-dom";
import {
  Fragment,
  type FormEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Button,
  NativeSelect,
  NativeSelectOption,
  useSwipeDismiss,
  type CandidateCardAction,
} from "../../../shared/ui";
import type { PlanTarget } from "../../../shared/api/types";
import { randomUUID } from "../../../shared/browser/randomUUID";
import type { CurationWorkspaceResponse } from "../domain/types";
import {
  buildRoundTargetIndex,
  latestRoundForTarget,
  resolveTargetWorkStatus,
} from "../domain/targetWork";
import {
  describeJobCompletion,
  selectActivitySlot,
  type ConversationDraft,
  type LocalActivity,
} from "../domain/conversation";
import {
  CurationAgentRunningPanel,
  CurationResultConfirmationPanel,
} from "./CurationAgentWork";
import {
  executeAutoResearchAction,
  retryIntelligenceJob,
} from "../infra/curationApi";
import { getAgencyOrderCapability } from "../../ordering/infra/agencyOrderApi";
import {
  LiveCatalogAPIError,
  formatMinor,
  hydrateCatalogResearch,
  loadCatalogCart,
  prepareAgencyOrderPreview,
  replaceCatalogCart,
  saveCatalogConfiguration,
  saveCatalogInteraction,
  type AgencyOrderPreparationPreview,
  type LiveCartItem,
  type LiveCatalogProviderMessage,
  type LiveCatalogProduct,
  type CatalogWorkspaceResponse,
  type LiveVariantRow,
} from "../research/infra/liveCatalogReviewApi";
import {
  CatalogCandidateModal,
  type CatalogCandidateView,
  type VariantInteraction,
  variantInteractionKey,
} from "./CatalogCandidateModal";
import "./catalog-curation-research.css";
import { useLocale, type Localize } from "../../../shared/i18n";

type LivePool = {
	version: number;
  products: LiveCatalogProduct[];
  hiddenProducts: LiveCatalogProduct[];
  messages: LiveCatalogProviderMessage[];
  latestMode: "REPLACE" | "APPEND";
  latestDurationMilliseconds: number;
  latestShopifyCalls: number;
  latestRateRemaining: number;
};

type SavedCandidateConfiguration = {
  variant: LiveVariantRow;
  observedAt: string;
  version: number;
};

const visibleTargetHydrationConcurrency = 5;
const catalogRateLimitReasons = new Set([
  "LIVE_CATALOG_REVIEW_BUSY",
  "LIVE_CATALOG_REVIEW_RATE_LIMITED",
  "RATE_LIMITED",
  "PROVIDER_RATE_LIMITED",
]);

type Props = {
  response: CurationWorkspaceResponse;
  working: boolean;
  cartOpenRequest: number;
  onAddTargets: (instruction: string) => Promise<void>;
  onResearchAgain: (targetId: string, feedback: string) => Promise<void>;
  onCartCountChange: (count: number) => void;
  onRemoveTarget: (targetId: string) => Promise<void>;
  onOpenOrderSheet?: (path: string) => void;
  conversationTail?: ReactNode;
  onConversationMessage: (message: ConversationDraft) => void;
  // Retry mutates Server state carried by the workspace response; without a
  // page re-read the Target keeps showing the failure until the next poll.
  onWorkChanged?: () => void | Promise<void>;
};

/** The sheet's first tab when there are several product groups: every group's candidates, one after another. */
const allTargetsTab = "*all";

export function CatalogCurationResearch({
  response,
  working,
  cartOpenRequest,
  onAddTargets,
  onResearchAgain,
  onCartCountChange,
  onRemoveTarget,
  onOpenOrderSheet,
  conversationTail,
  onConversationMessage,
  onWorkChanged,
}: Props) {
  const conversationActivity=useConversationActivity();
  const { l, locale } = useLocale();
  const threads = useCurationThreads();
  const [focus, setFocus] = useState<Focus>({ kind: "AUTO" });
  const [modeSelectorOpen, setModeSelectorOpen] = useState(false);
  const [autoResolutionMessage, setAutoResolutionMessage] = useState<string>();
  const [instruction, setInstruction] = useState("");
  const [livePools, setLivePools] = useState<Record<string, LivePool>>(() =>
    workspacePools(catalogResearchWorkspace(response)),
  );
  const [workspaceMessages, setWorkspaceMessages] = useState<LiveCatalogProviderMessage[]>([]);
  const [showHiddenTargets, setShowHiddenTargets] = useState<Record<string, boolean>>({});
  // A response shows one representative per Target; the Target's own section opens in place on request.
  // A product group's own surface is a sheet (ADR-0086); the conversation only shows its representative.
  const [sheetTargetId, setSheetTargetId] = useState<string>();
  const [directDetail, setDirectDetail] = useState<{ candidateId: string; targetId: string; nonce: number }>();
  const targetViewState = useTargetViews(response.curation.id);
  const [representatives,setRepresentatives]=useState<Record<string,string|undefined>>({});
  const rememberRepresentative=useCallback((targetId:string,candidateId?:string)=>{
    setRepresentatives(current=>current[targetId]===candidateId?current:{...current,[targetId]:candidateId});
  },[]);
  // A reply or a representative can ask a card that owns its own details (external malls, Amazon) to open them.
  const results = useConversationResults();
  const [expandOrdinals, setExpandOrdinals] = useState<Record<string, number>>({});
  const [liveWorkingTarget, setLiveWorkingTarget] = useState<string>();
  const [cartItems, setCartItems] = useState<LiveCartItem[]>([]);
  const [cartMarket, setCartMarket] = useState({ country: "US", currency: "USD" });
  const [cartVersion, setCartVersion] = useState(0);
  const [enrichmentLoading, setEnrichmentLoading] = useState(false);
  const [cartLoadState, setCartLoadState] = useState<
    "LOADING" | "READY" | "FAILED"
  >("LOADING");
  const [cartLoadAttempt, setCartLoadAttempt] = useState(0);
  const [catalogHydrationErrors, setCatalogHydrationErrors] = useState<Record<string, string>>({});
  const [hydratingVisibleTargets, setHydratingVisibleTargets] = useState<Record<string, boolean>>({});
  const [catalogHydrationAttempt, setCatalogHydrationAttempt] = useState(0);
  const [candidateHydrationErrors, setCandidateHydrationErrors] = useState<Record<string, string>>({});
  const [hydratingCandidates, setHydratingCandidates] = useState<Record<string, boolean>>({});
  const [cartSaving, setCartSaving] = useState(false);
  const [cartOpen, setCartOpen] = useState(false);
  const [candidateModal, setCandidateModal] = useState<CatalogCandidateView>();
  const [editingCartItemID, setEditingCartItemID] = useState<string>();
  // The saved option decides which ASIN an Amazon card acts on, and the card
  // mounts on the first render, so the workspace read seeds this too.
  const [configuredCandidates, setConfiguredCandidates] = useState<
    Record<string, SavedCandidateConfiguration>
  >(() => Object.fromEntries((response.catalogResearch?.configurations ?? []).map((configuration) => [
    configuration.candidateId,
    { variant: configuration.variant, observedAt: configuration.observedAt, version: configuration.version ?? 0 },
  ])));
  const [targetRemoval, setTargetRemoval] = useState<PlanTarget>();
  const [variantInteractions, setVariantInteractions] = useState<
    Record<string, VariantInteraction>
  >({});
  // Every external card's state arrives with the workspace read, so the grid
  // reads it once for the curation instead of once per card. A card mounts on
  // the first render, so this is derived there and not in an effect.
  const cardState = useMemo(
    () => externalCardState(response.catalogResearch),
    [response.catalogResearch],
  );
	const [preparing, setPreparing] = useState(false);
  const [orderCapability, setOrderCapability] = useState<
    "CHECKING" | "READY" | "UNAVAILABLE"
  >(onOpenOrderSheet ? "CHECKING" : "READY");
  const [preparation, setPreparation] =
    useState<AgencyOrderPreparationPreview>();
  const [draftError, setDraftError] = useState<string>();
  const [localActivity, setLocalActivity] = useState<LocalActivity>();
  const [retryingJobId, setRetryingJobId] = useState<string>();
  const [retryFailure, setRetryFailure] = useState<{
    jobId: string;
    message: string;
  }>();
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const liveSearchInFlightRef = useRef<Promise<void> | undefined>(undefined);
  const seenJobStatusRef = useRef<Map<string, string> | undefined>(undefined);
  const pendingManagedTurnRef = useRef<string | undefined>(undefined);

  function pushMessage(draft: ConversationDraft) {
    onConversationMessage(draft);
  }

  useEffect(() => {
    if (!cartOpen || !onOpenOrderSheet) return;
    let active = true;
    setOrderCapability("CHECKING");
    setDraftError(undefined);
    void getAgencyOrderCapability()
      .then(({ capability }) => {
        if (!active) return;
        setOrderCapability(capability.state === "READY" ? "READY" : "UNAVAILABLE");
        if (capability.state !== "READY") {
          setDraftError(l("Ordering is not available right now. Your cart is preserved; try again shortly or contact support.", "현재 주문 기능이 준비되지 않았습니다. 장바구니는 유지되며, 잠시 후 다시 시도하거나 지원팀에 문의할 수 있습니다."));
        }
      })
      .catch(() => {
        if (!active) return;
        setOrderCapability("UNAVAILABLE");
        setDraftError(l("We couldn't check ordering status. Your cart is preserved; check your connection and try again.", "주문 기능 상태를 확인할 수 없습니다. 장바구니는 유지되며, 연결을 확인한 뒤 다시 시도해 주세요."));
      });
    return () => { active = false; };
  }, [cartOpen, l, onOpenOrderSheet]);
  const enrichedPoolVersionsRef = useRef<Record<string, number>>({});
  const enrichedAmazonCandidatesRef = useRef(new Set<string>());
  const enrichedHiddenPoolVersionsRef = useRef<Record<string, number>>({});

  const country = response.plan.locationContext.country;
  // Budget currency belongs to research. Saved Shopify options and Cart use
  // the separate market returned by the Cart API.
  const currency = response.plan.totalBudget.currency;
  const durableCatalogWorkspace = catalogResearchWorkspace(response);
  const durableWorkspaceScope = durableCatalogWorkspace.pools
    .map((pool) => [
      pool.targetId,
      pool.version,
      pool.latestMode ?? "",
      ...pool.products.map(({ candidateId }) => candidateId),
      ...pool.hiddenProducts.map(({ candidateId }) => `hidden:${candidateId}`),
    ].join("/"))
    .join(":");
  const visibleEnrichmentScope = durableCatalogWorkspace.pools
    .map((pool) => [
      pool.targetId,
      pool.version,
      ...pool.products.map(({ candidateId }) => candidateId),
    ].join("/"))
    .join(":");
  const busy =
    working || conversationActivity.busy || Boolean(localActivity) || enrichmentLoading ||
    Boolean(liveWorkingTarget) || preparing || cartSaving;
  const intelligenceJobs = response.intelligence ?? [];
  const roundTargetIndex = useMemo(
    () => buildRoundTargetIndex(response.research.groups),
    [response.research.groups],
  );
  // Variant interactions are stored per candidateId::variantId; the grid badge
  // shows a candidate-level any-variant aggregate.
  const candidateSignals = useMemo(() => {
    const map: Record<
      string,
      { pinned: boolean; liked: boolean; disliked: boolean }
    > = {};
    for (const [key, value] of Object.entries(variantInteractions)) {
      const [candidateId] = key.split("::");
      const entry = (map[candidateId] ??= {
        pinned: false,
        liked: false,
        disliked: false,
      });
      entry.pinned ||= value.pinned;
      entry.liked ||= value.sentiment === "LIKE";
      entry.disliked ||= value.sentiment === "DISLIKE";
    }
    return map;
  }, [variantInteractions]);

  async function saveVariantReaction(key: string, value: VariantInteraction, likedSnapshot: CatalogLikedVariantSnapshot, relationToken?: string) {
    const [candidateId, variantId] = key.split("::");
    setLocalActivity({
      label: l("Save product reaction", "상품 반응 저장"),
      detail: l("Saving your pin and preference.", "핀과 선호도를 저장하고 있습니다."),
    });
    try {
      await saveCatalogInteraction({
        curationId: response.curation.id,
        candidateId,
        variantId,
        pinned: value.pinned,
        sentiment: value.sentiment,
        likedSnapshot,
        relationToken,
      });
    } catch (caught) {
      pushMessage({
        role: "VITLANE",
        title: l("We couldn't save your reaction", "반응을 저장하지 못했습니다"),
        body: l("Your previous pin and preference are unchanged. Try again.", "이전 핀과 선호도는 그대로 유지됩니다. 다시 시도해 주세요."),
      });
      throw caught;
    } finally {
      setLocalActivity(undefined);
    }
    setVariantInteractions((current) => ({ ...current, [key]: value }));
  }

  // Typed route actions already have a durable ACTION + RESULT transcript. A
  // completion message is needed only for an Auto turn that began locally;
  // this keeps old terminal jobs from resurfacing below the current artifact.
  useEffect(() => {
    const jobs = response.intelligence ?? [];
    if (!seenJobStatusRef.current) {
      seenJobStatusRef.current = new Map(
        jobs.map((job) => [job.jobId, job.status]),
      );
      return;
    }
    const seen = seenJobStatusRef.current;
    for (const job of jobs) {
      const before = seen.get(job.jobId);
      seen.set(job.jobId, job.status);
      if (before === job.status) continue;
      if (
        job.status === "SUCCEEDED" ||
        job.status === "FAILED" ||
        job.status === "CANCELLED"
      ) {
        const turnId = pendingManagedTurnRef.current;
        if (!turnId) continue;
        const targetId = job.targetKind === "RESEARCH_ROUND"
          ? roundTargetIndex.get(job.targetId)
          : undefined;
        const targetTitle = response.targets.find(({ id }) => id === targetId)?.title;
        pushMessage({
          ...describeJobCompletion(job, {
            roundTargetIndex,
            groups: response.research.groups,
            targets: response.targets,
          }),
          state: "SETTLED",
          turnId,
          reconcilesServerAction: true,
          diff: job.status === "SUCCEEDED" && targetTitle
            ? { changed: [targetTitle] }
            : undefined,
        });
        pendingManagedTurnRef.current = undefined;
      }
    }
  }, [response]);

  function retryTargetResearch(jobId: string) {
    setRetryingJobId(jobId);
    setRetryFailure(undefined);
    retryIntelligenceJob(jobId)
      .then(() => onWorkChanged?.())
      .catch(() =>
        setRetryFailure({
          jobId,
          message: l("We couldn't retry. Check again shortly.", "다시 시도하지 못했습니다. 잠시 후 다시 확인해 주세요."),
        }),
      )
      .finally(() => setRetryingJobId(undefined));
  }

  useEffect(() => {
    onCartCountChange(cartItems.length);
  }, [cartItems.length, onCartCountChange]);

  useEffect(() => {
    if (cartOpenRequest > 0) setCartOpen(true);
  }, [cartOpenRequest]);

  useEffect(() => {
    const workspace = catalogResearchWorkspace(response);
    setLivePools((current) => mergeStoredWorkspacePools(current, workspace));
    setExpandOrdinals(Object.fromEntries(
      workspace.pools.map((pool) => [pool.targetId, pool.expandOrdinal]),
    ));
    setConfiguredCandidates(Object.fromEntries(
      workspace.configurations.map((configuration) => [
        configuration.candidateId,
        { variant: configuration.variant, observedAt: configuration.observedAt, version: configuration.version ?? 0 },
      ]),
    ));
    setVariantInteractions(Object.fromEntries(
      workspace.interactions.map((interaction) => [
        variantInteractionKey(interaction.candidateId, interaction.variantId),
        { pinned: interaction.pinned, sentiment: interaction.sentiment },
      ]),
    ));
  }, [durableWorkspaceScope]);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    setCartLoadState("LOADING");
    void loadCatalogCart(response.curation.id, controller.signal)
      .then((cart) => {
        if (!active) return;
        setCartItems(cart.items);
        setCartMarket({ country: cart.country, currency: cart.currency });
        setCartVersion(cart.version);
        setCartLoadState("READY");
        setDraftError(undefined);
      })
      .catch((caught) => {
        if (!active || isAbortError(caught)) return;
        setCartLoadState("FAILED");
        setDraftError(
          l("We couldn't load your cart. Reload it before adding or changing items.", "장바구니를 불러오지 못했습니다. 상품을 담거나 변경하기 전에 다시 불러와 주세요."),
        );
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [response.curation.id, cartLoadAttempt, l]);

  useEffect(() => {
    const targets = durableCatalogWorkspace.pools.filter(
      (pool) =>
        pool.products.some((p) => !p.source || p.source === "SHOPIFY") &&
        enrichedPoolVersionsRef.current[pool.targetId] !== pool.version,
    );
    if (!visibleEnrichmentScope || targets.length === 0) return;
    let active = true;
    const controller = new AbortController();
    setEnrichmentLoading(true);
    setHydratingVisibleTargets((current) => ({
      ...current,
      ...Object.fromEntries(targets.map(({ targetId }) => [targetId, true])),
    }));
    setCatalogHydrationErrors((current) => {
      const next = { ...current };
      for (const { targetId } of targets) delete next[targetId];
      return next;
    });
    let nextTargetIndex = 0;
    const hydrateNextTarget = async () => {
      while (active) {
        const target = targets[nextTargetIndex++];
        if (!target) return;
        try {
          const workspace = await hydrateCatalogResearch({
            curationId: response.curation.id,
            scope: "VISIBLE_TARGET",
            targetId: target.targetId,
            signal: controller.signal,
          });
          if (!active) return;
          const hydrated = workspace.pools.find(
            ({ targetId }) => targetId === target.targetId,
          );
          if (!hydrated) {
            throw new LiveCatalogAPIError({
              code: "INTERNAL_FAILURE",
              reasonCode: "CATALOG_RESEARCH_VISIBLE_TARGET_UNAVAILABLE",
              retryable: true,
            });
          }
          const hydratedPool = workspacePools({
            ...workspace,
            pools: [hydrated],
          })[target.targetId];
          setLivePools((current) => ({
            ...current,
            [target.targetId]: {
              ...hydratedPool,
 products: [...hydratedPool.products, ...(current[target.targetId]?.products.filter((p) => p.source && p.source !== "SHOPIFY") ?? [])],
              hiddenProducts:
                current[target.targetId]?.hiddenProducts ?? hydratedPool.hiddenProducts,
            },
          }));
          enrichedPoolVersionsRef.current[target.targetId] = hydrated.version;
          setWorkspaceMessages((current) => uniqueCatalogMessages([
            ...current,
            ...(workspace.messages ?? []),
          ]));
          setConfiguredCandidates((current) => ({
            ...current,
            ...Object.fromEntries(workspace.configurations.map((configuration) => [
              configuration.candidateId,
              { variant: configuration.variant, observedAt: configuration.observedAt, version: configuration.version ?? 0 },
            ])),
          }));
          setVariantInteractions((current) => ({
            ...current,
            ...Object.fromEntries(workspace.interactions.map((interaction) => [
              variantInteractionKey(interaction.candidateId, interaction.variantId),
              { pinned: interaction.pinned, sentiment: interaction.sentiment },
            ])),
          }));
          setExpandOrdinals((current) => ({
            ...current,
            [target.targetId]: hydrated.expandOrdinal,
          }));
        } catch (caught) {
          if (!active || isAbortError(caught)) return;
          setCatalogHydrationErrors((current) => ({
            ...current,
            [target.targetId]: caught instanceof LiveCatalogAPIError
              ? caught.fault.reasonCode
              : "PHASE8_SHOPIFY_DISPLAY_UNAVAILABLE",
          }));
        } finally {
          if (active) {
            setHydratingVisibleTargets((current) => ({
              ...current,
              [target.targetId]: false,
            }));
          }
        }
      }
    };
    const workers = Array.from(
      { length: Math.min(visibleTargetHydrationConcurrency - (durableCatalogWorkspace.pools.some((p) => p.products.some((v) => v.source === "AMAZON")) ? 1 : 0), targets.length) },
      () => hydrateNextTarget(),
    );
    void Promise.all(workers).finally(() => {
      if (active) setEnrichmentLoading(false);
    });
    return () => {
      active = false;
      controller.abort();
    };
    // Action/Round/Job polling does not change this signature. Shopify is read
    // only after the durable visible Candidate projection changes.
  }, [response.curation.id, visibleEnrichmentScope, catalogHydrationAttempt]);

  const amazonHydrationCandidates = durableCatalogWorkspace.pools.flatMap((pool) =>
    [...pool.products, ...(showHiddenTargets[pool.targetId] ? pool.hiddenProducts : [])]
      .filter((product) => product.source === "AMAZON")
      .map((product) => ({ candidateId: product.candidateId, targetId: pool.targetId,
        key: `${response.curation.id}/${pool.targetId}/${pool.version}/${product.candidateId}` })),
  );
  const amazonHydrationScope = amazonHydrationCandidates.map(({ key }) => key).join("|");
  useEffect(() => {
    const controller = new AbortController();
    void (async () => {
      for (const candidate of amazonHydrationCandidates) {
        if (controller.signal.aborted) return;
        if (enrichedAmazonCandidatesRef.current.has(candidate.key)) continue;
        let product: LiveCatalogProduct | undefined;
        try {
          const workspace = await queueAmazonRead(() => hydrateCatalogResearch({
            curationId: response.curation.id, targetId: candidate.targetId,
            candidateId: candidate.candidateId, source: "AMAZON", scope: "CANDIDATE",
            signal: controller.signal,
          }), controller.signal);
          product = workspace.pools.flatMap((pool) => [...pool.products, ...pool.hiddenProducts])
            .find((value) => value.candidateId === candidate.candidateId);
        } catch { /* Keep the saved Candidate and expose its explicit retry. */ }
        if (controller.signal.aborted) return;
        enrichedAmazonCandidatesRef.current.add(candidate.key);
        setLivePools((current) => {
          const pool = current[candidate.targetId];
          if (!pool) return current;
          const update = (old: LiveCatalogProduct) => old.candidateId !== candidate.candidateId ? old
            : product ?? { ...old, hydration: { status: "FAILED" as const, retryable: true } };
          return { ...current, [candidate.targetId]: { ...pool,
            products: pool.products.map(update), hiddenProducts: pool.hiddenProducts.map(update),
          } };
        });
      }
    })();
    return () => controller.abort();
  }, [amazonHydrationScope]);

  async function toggleHiddenTarget(targetId: string, showHidden: boolean) {
    if (showHidden) {
      setShowHiddenTargets((current) => ({ ...current, [targetId]: false }));
      return;
    }
    const pool = livePools[targetId];
    if (!pool || pool.hiddenProducts.length === 0) return;
    if (pool.hiddenProducts.every((product) => product.source && product.source !== "SHOPIFY")) {
      setShowHiddenTargets((current) => ({ ...current, [targetId]: true }));
      return;
    }
    if (enrichedHiddenPoolVersionsRef.current[targetId] === pool.version) {
      setShowHiddenTargets((current) => ({ ...current, [targetId]: true }));
      return;
    }
    setEnrichmentLoading(true);
    setCatalogHydrationErrors((current) => {
      const next = { ...current };
      delete next[targetId];
      return next;
    });
    try {
      const workspace = await hydrateCatalogResearch({
        curationId: response.curation.id,
        scope: "HIDDEN_TARGET",
        targetId,
      });
      const hydrated = workspace.pools.find((value) => value.targetId === targetId);
      if (!hydrated) {
        throw new LiveCatalogAPIError({
          code: "INTERNAL_FAILURE",
          reasonCode: "CATALOG_RESEARCH_HIDDEN_TARGET_UNAVAILABLE",
          retryable: true,
        });
      }
      const hydratedPool = workspacePools({ ...workspace, pools: [hydrated] })[targetId];
      setLivePools((current) => ({
        ...current,
        [targetId]: {
          ...hydratedPool,
 hiddenProducts: [...hydratedPool.hiddenProducts, ...(current[targetId]?.hiddenProducts.filter((p) => p.source && p.source !== "SHOPIFY") ?? [])],
          products: current[targetId]?.products ?? hydratedPool.products,
        },
      }));
      setConfiguredCandidates((current) => ({
        ...current,
        ...Object.fromEntries(workspace.configurations.map((configuration) => [
          configuration.candidateId,
          { variant: configuration.variant, observedAt: configuration.observedAt, version: configuration.version ?? 0 },
        ])),
      }));
      enrichedHiddenPoolVersionsRef.current[targetId] = hydrated.version;
      setShowHiddenTargets((current) => ({ ...current, [targetId]: true }));
    } catch (caught) {
      if (isAbortError(caught)) return;
      setCatalogHydrationErrors((current) => ({
        ...current,
        [targetId]: caught instanceof LiveCatalogAPIError
          ? caught.fault.reasonCode
          : "CATALOG_RESEARCH_HIDDEN_HYDRATION_FAILED",
      }));
    } finally {
      setEnrichmentLoading(false);
    }
  }

  async function retryCandidateHydration(targetId: string, candidateId: string) {
    setHydratingCandidates((current) => ({ ...current, [candidateId]: true }));
    setCandidateHydrationErrors((current) => {
      const next = { ...current };
      delete next[candidateId];
      return next;
    });
    try {
      const workspace = await hydrateCatalogResearch({
        curationId: response.curation.id,
        scope: "CANDIDATE",
        targetId,
        candidateId,
      });
      const hydrated = workspace.pools.find((pool) => pool.targetId === targetId);
      const hydratedPool = hydrated
        ? workspacePools({ ...workspace, pools: [hydrated] })[targetId]
        : undefined;
      const candidate = hydratedPool
        ? [...hydratedPool.products, ...hydratedPool.hiddenProducts].find(
            (product) => product.candidateId === candidateId,
          )
        : undefined;
      if (!hydratedPool || !candidate) {
        throw new LiveCatalogAPIError({
          code: "INTERNAL_FAILURE",
          reasonCode: "CATALOG_RESEARCH_CANDIDATE_UNAVAILABLE",
          retryable: true,
        });
      }
      setLivePools((current) => {
        const pool = current[targetId];
        if (!pool) return current;
        return {
          ...current,
          [targetId]: {
            ...pool,
            products: mergeFreshCatalogProducts(pool.products, hydratedPool.products),
            hiddenProducts: mergeFreshCatalogProducts(
              pool.hiddenProducts,
              hydratedPool.hiddenProducts,
            ),
            messages: uniqueCatalogMessages([
              ...pool.messages.filter(
                (message) => message.subjectRef !== candidateId,
              ),
              ...hydratedPool.messages,
            ]),
          },
        };
      });
      setWorkspaceMessages((current) => uniqueCatalogMessages([
        ...current,
        ...(workspace.messages ?? []),
      ]));
      setConfiguredCandidates((current) => ({
        ...current,
        ...Object.fromEntries(workspace.configurations.map((configuration) => [
          configuration.candidateId,
          { variant: configuration.variant, observedAt: configuration.observedAt, version: configuration.version ?? 0 },
        ])),
      }));
      setVariantInteractions((current) => ({
        ...current,
        ...Object.fromEntries(workspace.interactions.map((interaction) => [
          variantInteractionKey(interaction.candidateId, interaction.variantId),
          { pinned: interaction.pinned, sentiment: interaction.sentiment },
        ])),
      }));
    } catch (caught) {
      if (isAbortError(caught)) return;
      setCandidateHydrationErrors((current) => ({
        ...current,
        [candidateId]: caught instanceof LiveCatalogAPIError
          ? caught.fault.reasonCode
          : "CATALOG_RESEARCH_CANDIDATE_HYDRATION_FAILED",
      }));
    } finally {
      setHydratingCandidates((current) => ({ ...current, [candidateId]: false }));
    }
  }

  function selectFocus(next: Focus) {
    setFocus(next);
    setModeSelectorOpen(false);
    setAutoResolutionMessage(undefined);
    window.requestAnimationFrame(() => composerRef.current?.focus());
  }

  useEffect(() => {
    if (!threads?.ready) return;
    if (threads.mode.mode === "AUTO") setFocus({ kind: "AUTO" });
    else setFocus(current => current.kind !== "AUTO" ? current : response.targets.length === 1 ? { kind: "RESEARCH_AGAIN", target: response.targets[0] } : { kind: "ADD_TARGET" });
  }, [threads?.mode.mode, threads?.ready]);

  async function submitComposer(event: FormEvent<HTMLFormElement>) {
 event.preventDefault();
 if(conversationActivity.busy)return;
 conversationActivity.setBusy(true);
 try{await doSubmitComposer(event);}finally{conversationActivity.setBusy(false);}
 }
 async function doSubmitComposer(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy || threads?.busy) return;
    const value = instruction.trim();
    if (threads && focus.kind!=="RETRY") {
      try { await threads.submit(value, response.curation.version, focus.kind === "AUTO" ? undefined : focus.kind, focus.kind === "RESEARCH_AGAIN" ? focus.target.id : undefined); setInstruction(""); await onWorkChanged?.(); }
      catch (caught) { pushMessage({ role: "VITLANE", title: l("Request could not be started", "요청을 시작하지 못했습니다"), body: messageOf(caught, l) }); }
      return;
    }
    if (!value && focus.kind !== "RESEARCH_AGAIN" && focus.kind !== "RETRY") {
      pushMessage({
        role: "VITLANE",
        title: l("Tell us a little more", "요청을 조금 더 알려주세요"),
        body: l("Enter a natural-language request after the {focus} tag.", "{focus} 태그 뒤에 자연어 요청을 입력해 주세요.", { focus: focusLabel(focus, l) }),
      });
      return;
    }
    if(focus.kind==="RETRY") {
     try{await executeConversationRequest({expectedConversationVersion:response.conversation?.version,curationId:response.curation.id,expectedCurationVersion:response.curation.version,mode:"RETRY",request:"",jobId:focus.jobId});setFocus(threads?.mode.mode==="MANUAL"?{kind:"ADD_TARGET"}:{kind:"AUTO"});await threads?.reload();await onWorkChanged?.();}
     catch(caught){pushMessage({role:"VITLANE",title:l("Retry unavailable","재시도할 수 없어요"),body:messageOf(caught,l)});}
     return;
    }
    if (focus.kind === "AUTO") {
      const turnId = `phase8-turn:${randomUUID()}`;
      pushMessage({
        role: "USER",
        title: l("Me", "나"),
        body: value,
        state: "ACTIVE",
        turnId,
        reconcilesServerAction: true,
      });
      setLocalActivity({
        label: l("Interpreting Auto request", "Auto 요청 해석 중"),
        detail: l(
          "Vitlane is deciding how to continue your product research.",
          "Vitlane이 상품 조사를 어떻게 이어갈지 확인하고 있습니다.",
        ),
      });
      try {
        const result = await executeAutoResearchAction({
 expectedConversationVersion:response.conversation?.version,
          curationId: response.curation.id,
          request: value,
          expectedCurationVersion: response.curation.version,
        });
        if(response.conversation && result.status!=="EXECUTED"){
         setInstruction("");setFocus({kind:"AUTO"});await onWorkChanged?.();return;
        }
        if (result.reasonCode === "BUDGET_SETTINGS_ONLY") {
          pushMessage({ role: "VITLANE", title: l("Budget settings", "예산 설정"), body: l("Edit budgets using the Budget button above the input.", "예산은 입력창 위 예산 버튼에서 변경해 주세요."), state: "SETTLED", turnId });
          setInstruction("");
          return;
        }
        if (result.status === "NEEDS_SELECTION") {
          setAutoResolutionMessage(l(
            "Choose a product to research again, or choose Add product.",
            "다시 조사할 상품을 선택하거나 상품 추가를 선택해 주세요.",
          ));
          pushMessage({
            role: "VITLANE",
            title: l("Choose where to continue", "이어갈 위치를 선택해 주세요"),
            body: l(
              "Choose a product to research again, or choose Add product.",
              "다시 조사할 상품을 선택하거나 상품 추가를 선택해 주세요.",
            ),
            state: "SETTLED",
            turnId,
          });
          setModeSelectorOpen(true);
          return;
        }
        const target = result.targetId
          ? response.targets.find(({ id }) => id === result.targetId)
          : undefined;
        pushMessage({
          role: "VITLANE",
          title: l("Auto request accepted", "Auto 요청을 접수했습니다"),
          body: result.decision === "RESEARCH_AGAIN"
            ? l(
                "We’ll research {target} again using your request.",
                "요청하신 내용으로 {target}을 다시 조사합니다.",
                { target: target?.title ?? l("the selected product", "선택한 상품") },
              )
            : l(
                "We’ll add a new product to this research.",
                "새 상품을 조사 목록에 추가합니다.",
              ),
          state: "ACTIVE",
          turnId,
        });
        pendingManagedTurnRef.current = turnId;
        setFocus({ kind: "AUTO" });
        setAutoResolutionMessage(undefined);
        setInstruction("");
        await onWorkChanged?.();
      } catch (caught) {
        if (pendingManagedTurnRef.current === turnId) {
          pendingManagedTurnRef.current = undefined;
        }
        pushMessage({
          role: "VITLANE",
          title: l("We couldn't interpret the Auto request", "Auto 요청을 해석하지 못했습니다"),
          body: messageOf(caught, l),
          state: "SETTLED",
          turnId,
        });
      } finally {
        setLocalActivity(undefined);
      }
      return;
    }
    if (focus.kind === "ADD_TARGET") {
      try {
        await onAddTargets(value);
        setFocus({ kind: "AUTO" });
        setAutoResolutionMessage(undefined);
        setInstruction("");
      } catch (caught) {
        pushMessage({
          role: "VITLANE",
          title: l("We couldn't add the product", "상품을 추가하지 못했습니다"),
          body: messageOf(caught, l),
        });
      }
      return;
    }
    if (focus.kind === "RESEARCH_AGAIN") {
      try {
        await onResearchAgain(focus.target.id, value);
        setFocus({ kind: "AUTO" });
        setAutoResolutionMessage(undefined);
        setInstruction("");
      } catch (caught) {
        pushMessage({
          role: "VITLANE",
          title: l("We couldn't start the research", "재조사를 시작하지 못했습니다"),
          body: messageOf(caught, l),
        });
      }
      return;
    }
  }

  async function replaceCart(
    nextItems: LiveCartItem[],
    activityLabel = l("Save cart", "장바구니 저장"),
    announceSuccess = true,
  ) {
    if (cartLoadState !== "READY") {
      throw new LiveCatalogAPIError({
        code: "CONFLICT",
        reasonCode: "PHASE8_CART_NOT_READY",
        retryable: true,
      });
    }
    setCartSaving(true);
    setLocalActivity({
      label: activityLabel,
      detail: l("Saving your cart.", "장바구니를 저장하고 있습니다."),
    });
    try {
      const result = await replaceCatalogCart(
        response.curation.id,
        cartVersion,
        nextItems,
      );
      setCartItems(result.items);
      setCartMarket({ country: result.country, currency: result.currency });
      setCartVersion(result.version);
      setPreparation(undefined);
      setDraftError(undefined);
      if (announceSuccess) {
        pushMessage({
          role: "VITLANE",
          title: activityLabel,
          body: l("Saved {count} items in your cart.", "장바구니에 상품 {count}개를 저장했습니다.", { count: result.items.length }),
        });
      }
    } catch (caught) {
      const reason = caught instanceof LiveCatalogAPIError
        ? caught.fault.reasonCode
        : "PHASE8_CART_SAVE_FAILED";
      if (reason === "PHASE8_CART_VERSION_CONFLICT") {
        setCartLoadState("LOADING");
        try {
          const current = await loadCatalogCart(response.curation.id);
          setCartItems(current.items);
          setCartMarket({ country: current.country, currency: current.currency });
          setCartVersion(current.version);
          setCartLoadState("READY");
          setDraftError(
            l("The cart changed in another window, so we loaded the latest state. Review it and save again.", "장바구니가 다른 화면에서 변경되어 최신 상태를 불러왔습니다. 내용을 확인하고 저장을 다시 눌러 주세요."),
          );
        } catch {
          setCartLoadState("FAILED");
          setDraftError(
            l("We couldn't reload the latest cart. Reload it before continuing.", "최신 장바구니를 불러오지 못했습니다. 계속하기 전에 다시 불러와 주세요."),
          );
        }
      } else {
        setDraftError(
          l("We couldn't save your cart. Review the item and try again.", "장바구니를 저장하지 못했습니다. 상품을 확인한 뒤 다시 시도해 주세요."),
        );
      }
      pushMessage({
        role: "VITLANE",
        title: activityLabel,
        body: reason === "PHASE8_CART_VERSION_CONFLICT"
          ? l("Your cart changed in another window. We loaded the latest contents; review them and save again.", "다른 화면에서 장바구니가 변경되었습니다. 최신 내용을 불러왔으니 확인하고 다시 저장해 주세요.")
          : l("We couldn't save your cart. Its previous contents are unchanged.", "장바구니를 저장하지 못했습니다. 이전 내용은 그대로 유지됩니다."),
      });
      throw caught;
    } finally {
      setCartSaving(false);
      setLocalActivity(undefined);
    }
  }

  async function addCartItem(
    candidate: CatalogCandidateView,
    variant: LiveVariantRow,
    observedAt: string,
  ) {
    if (!variant.available) {
      throw new LiveCatalogAPIError({
        code: "CONFLICT",
        reasonCode: "VARIANT_UNAVAILABLE",
        retryable: false,
      });
    }
    const cartItem: LiveCartItem = {
      cartItemId: `cart:${candidate.candidateId}:${variant.variantId}`,
      targetId: candidate.targetId,
      candidateId: candidate.candidateId,
      productTitle: candidate.title,
      productUrl: variant.productUrl || candidate.productUrl,
      productImageUrl: variant.mediaUrl || candidate.mediaUrl,
      merchantName: candidate.merchant,
      sellerDomain: candidate.sellerDomain,
      intentPoint: candidate.intentPoint,
      variantId: variant.variantId,
      variantTitle: variant.title,
      selectedOptions: variant.selectedOptions.map(
        ({ name, value }) => `${name}: ${value}`,
      ),
      previewPriceMinor: variant.priceMinor,
      previewCurrency: variant.currency,
      quantity: 1,
      observedAt,
    };
    const matchingItem = editingCartItemID
      ? undefined
      : cartItems.find((item) => item.cartItemId === cartItem.cartItemId);
    const nextItems = matchingItem
      ? cartItems.map((item) =>
          item.cartItemId === matchingItem.cartItemId
            ? { ...item, quantity: item.quantity + 1 }
            : item,
        )
      : [
          ...cartItems.filter(
            (item) =>
              item.cartItemId !== cartItem.cartItemId &&
              item.cartItemId !== editingCartItemID,
          ),
          cartItem,
        ];
    await replaceCart(
      nextItems,
      matchingItem ? l("Increase cart quantity", "장바구니 수량 추가") : l("Add to cart", "장바구니 담기"),
      false,
    );
    pushMessage({
      role: "VITLANE",
      title: l("Added to cart", "장바구니에 담았습니다"),
      body: l("{candidate} · {variant}. Price and availability will be confirmed before checkout.", "{candidate} · {variant}. 가격과 구매 가능 여부는 결제 전에 확인합니다.", { candidate: candidate.title, variant: variant.title }),
    });
    setCandidateModal(undefined);
    setEditingCartItemID(undefined);
  }

  async function saveCandidateOption(
    candidate: CatalogCandidateView,
    variant: LiveVariantRow,
    observedAt: string,
  ) {
    setLocalActivity({
      label: l("Save product option", "상품 옵션 저장"),
      detail: l("Saving the selected option for this product.", "이 상품에 선택한 옵션을 저장하고 있습니다."),
    });
    try {
      await saveCatalogConfiguration({
        curationId: response.curation.id,
        candidateId: candidate.candidateId,
        variant,
        observedAt,
      });
    } catch (caught) {
      pushMessage({
        role: "VITLANE",
        title: l("We couldn't save the product option", "상품 옵션을 저장하지 못했습니다"),
        body: l("The previously saved option is unchanged. Try again.", "이전에 저장한 옵션은 그대로 유지됩니다. 다시 시도해 주세요."),
      });
      throw caught;
    } finally {
      setLocalActivity(undefined);
    }
    setConfiguredCandidates((current) => ({
      ...current,
      // The save succeeded, so the stored option stands one version ahead.
      [candidate.candidateId]: { variant, observedAt, version: (current[candidate.candidateId]?.version ?? 0) + 1 },
    }));
    pushMessage({
      role: "VITLANE",
      title: l("Product option saved", "상품 옵션을 저장했습니다"),
      body: l("{candidate} · {variant}. The card now shows your saved option.", "{candidate} · {variant}. 카드에 저장한 옵션을 표시합니다.", { candidate: candidate.title, variant: variant.title }),
    });
    setCandidateModal(undefined);
    setEditingCartItemID(undefined);
  }

  const targetViews = response.targets;
  const activitySlot = selectActivitySlot({
    localActivity,
    jobs: intelligenceJobs,
    activeWork: response.activeWork,
  });

  const activityJobId = "job" in activitySlot ? activitySlot.job?.jobId : undefined;
  const threadOwnsActivity = Boolean(threads?.active || (activityJobId && threads?.threads.some(t => t.actions.some(a => a.jobs.some(j => j.jobId === activityJobId)))));

  async function prepareOrder() {
    if (cartItems.length === 0 || preparing) return;
	if (onOpenOrderSheet) {
		if (orderCapability !== "READY") {
			setDraftError(orderCapability === "CHECKING"
					? l("Checking ordering status. Try again shortly.", "주문 기능 상태를 확인하고 있습니다. 잠시 후 다시 시도해 주세요.")
					: l("Ordering is not available right now. Your cart is unchanged.", "현재 주문 기능이 준비되지 않았습니다. 장바구니는 그대로 유지됩니다."));
			return;
		}
		setCartOpen(false);
		onOpenOrderSheet(`/curations/${response.curation.id}/order-sheet?cartVersion=${cartVersion}`);
		return;
	}
	// Standalone component tests and the retained Step 1 preview exercise the
	// read-only preparation boundary without needing a Router. Product wiring
	// always supplies onOpenOrderSheet and therefore takes the Step 2 route.
	setPreparing(true);
	setDraftError(undefined);
	try {
		const result = await prepareAgencyOrderPreview(response.curation.id);
		setPreparation(result);
	} catch (caught) {
		setDraftError(caught instanceof LiveCatalogAPIError
			? l("We couldn't confirm price and availability. Review your cart and try again.", "가격과 재고를 확인하지 못했습니다. 장바구니를 확인한 뒤 다시 시도해 주세요.")
			: l("We couldn't reach Vitlane. Check your connection and try again.", "Vitlane에 연결하지 못했습니다. 연결 상태를 확인한 뒤 다시 시도해 주세요."));
	} finally {
		setPreparing(false);
	}
  }

  const criteriaView = useTargetCriteria(response.curation.id, response.targets.map(t => t.id), `${threads?.revision ?? ""}:${intelligenceJobs.map(j => `${j.jobId}:${j.status}`).join(",")}`);
  // What a Shopify candidate shows and does, whether it is a card in the rail or the representative of
  // its Target in a response: one source, so the two can never disagree about cart or loading state.
  const shopifyCard = (product: LiveCatalogProduct, target: PlanTarget, livePool: LivePool | undefined) => {
    const observedHydrationStatus = catalogProductHydrationStatus(product);
    const hydrationStatus =
      observedHydrationStatus === "LOADING" &&
        catalogHydrationErrors[target.id]
        ? "FAILED"
        : observedHydrationStatus;
    const hydrated = hydrationStatus === "READY";
    const unresolved = hydrationStatus === "UNRESOLVED";
    const hydrationFailed = hydrationStatus === "FAILED";
    const hydrationUnavailable = unresolved || hydrationFailed;
    const candidateHydrationError =
      candidateHydrationErrors[product.candidateId];
    const savedConfiguration =
      configuredCandidates[product.candidateId];
    const candidateView = liveCandidateView(
      product,
      target,
      response.curation.id,
      savedConfiguration,
      catalogProductDisplayMessages(
        livePool?.messages ?? [],
        product.candidateId,
      ),
      l,
    );
    const inCart = cartItems.some(
      ({ candidateId }) => candidateId === product.candidateId,
    );
    return {
      candidateView, savedConfiguration, hydrated, inCart,
      candidate: { ...shopifyPresentation(candidateView, product, Boolean(savedConfiguration)), recommendation: hydrationFailed
                            ? l("We couldn't load this product's details. Your saved recommendation is unchanged.", "이 상품의 상세 정보를 불러오지 못했습니다. 저장된 추천 상품은 그대로입니다.")
                            : unresolved ? l("Shopify could not resolve this saved product. The other recommendations remain available.", "Shopify에서 이 저장 상품을 확인하지 못했습니다. 다른 추천 상품은 계속 이용할 수 있습니다.") : candidateView.intentPoint },
      state: (hydrationUnavailable
                            ? "unavailable"
                            : hydrated
                            ? (inCart ? "in-cart" : "default")
                            : "loading") as "unavailable" | "in-cart" | "default" | "loading",
      onOpen: hydrated
                            ? () => {
                                targetViewState.recordViewed(target.id, product.candidateId);
                                setEditingCartItemID(undefined);
                                setCandidateModal(candidateView);
                              }
                            : undefined,
      primaryAction: (hydrationUnavailable
                            ? {
                                label: l("Try this product again", "이 상품 다시 시도"),
                                busy: Boolean(hydratingCandidates[product.candidateId]),
                                disabled: busy || product.hydration?.retryable === false,
                                onAction: () => void retryCandidateHydration(
                                  target.id,
                                  product.candidateId,
                                ),
                              }
                            : inCart
                            ? {
                                label: l("Remove from cart", "장바구니 제거"),
                                emphasis: "danger",
                                disabled: busy,
                                onAction: () => {
                                  void replaceCart(
                                    cartItems.filter(
                                      ({ candidateId }) =>
                                        candidateId !== product.candidateId,
                                    ),
                                    l("Remove from cart", "장바구니에서 제거"),
                                  ).catch(() => undefined);
                                },
                              }
                            : {
                                label: l("Add to cart", "장바구니 담기"),
                                disabled: busy || cartLoadState !== "READY",
                                onAction: () => {
                                  setEditingCartItemID(undefined);
                                  if (!savedConfiguration?.variant.available) {
                                    setCandidateModal(candidateView);
                                    return;
                                  }
                                  void addCartItem(
                                    candidateView,
                                    savedConfiguration.variant,
                                    savedConfiguration.observedAt,
                                  ).catch(() => undefined);
                                },
                              }) as CandidateCardAction,
      statusMessage: candidateHydrationError
                            ? l(
                                "We couldn't refresh this product. Try it again later.",
                                "이 상품 정보를 갱신하지 못했습니다. 잠시 후 다시 시도해 주세요.",
                              )
                            : hydrationFailed
                            ? l(
                                "Loading stopped. You can retry this product.",
                                "불러오기가 중단되었습니다. 이 상품만 다시 시도할 수 있습니다.",
                              )
                            : unresolved
                            ? l(
                                "This product lookup finished without details.",
                                "이 상품 조회는 상세 정보 없이 완료되었습니다.",
                              )
                            : undefined,
    };
  };
  // A card that owns its details reports when they open: the product becomes what the conversation shows.
  const productOpened = (targetId: string, candidateId: string) => targetViewState.recordViewed(targetId, candidateId);
  // One Target's own surface, shown in its sheet: criteria, sort, every candidate, the Target's state,
  // and at the end what its research looked at and the way to take the product group out.
  // `overview` is the sheet's "All" tab: the same candidates under a heading per product group, without the
  // group's own tools (criteria, sort, hidden products, removal) — those live in the group's tab, one click away.
  const candidatePrice = (product: LiveCatalogProduct) => productPrice(product, configuredCandidates[product.candidateId]?.variant);
  const targetSheetBody = (target: PlanTarget, overview = false) => {
          const livePool = livePools[target.id];
          const storedPool = durableCatalogWorkspace.pools.find((pool) => pool.targetId === target.id);
          const latestRound = latestRoundForTarget(response.research.groups, target.id);
          const noticeScope = JSON.stringify([response.curation.id, target.id, latestRound?.id, storedPool?.expandOrdinal]);
          const workStatus = resolveTargetWorkStatus({
            targetId: target.id,
            syncWorkingTargetId: liveWorkingTarget,
            jobs: intelligenceJobs,
            roundTargetIndex,
            latestRound,
          });
          const showHidden = Boolean(showHiddenTargets[target.id]);
          const liveProducts = livePool
            ? uniqueProducts([
                ...livePool.products,
                ...(showHidden ? livePool.hiddenProducts : []),
              ])
            : [];
          const totalPoolCount = uniqueCandidateCount(
            livePool?.products ?? [],
            livePool?.hiddenProducts ?? [],
          );
          const visibleCount = liveProducts.length;
          const hiddenCount = Math.max(0, totalPoolCount - visibleCount);
          const targetViewId = `phase8-target-view-${target.id}`;
          const hiddenToggle = showHidden || hiddenCount > 0 ? <Button
            type="button"
            size="compact"
            emphasis="quiet"
            className="curation-target-sheet__hidden"
            aria-pressed={showHidden}
            aria-label={showHidden ? l("Hide hidden products for {title} again", "{title} 숨긴 상품 다시 숨기기", { title: target.title }) : l("Show hidden products for {title}", "{title} 숨긴 상품 보기", { title: target.title })}
            disabled={busy}
            onClick={() => void toggleHiddenTarget(target.id, showHidden)}
          >
            {showHidden ? <EyeOff size={15} aria-hidden="true" /> : <Eye size={15} aria-hidden="true" />}
            <span>{showHidden ? l("Default view", "기본 보기") : l("Hidden {count}", "숨긴 후보 {count}", { count: hiddenCount })}</span>
          </Button> : null;
          const holder = requestThreads.find(t => t.id === lastResearch[target.id]);
          const targetFacts = holder ? comparisonFacts(holder, [target.id]) : undefined;
          return (
            <BudgetTargetContext.Provider key={target.id} value={target.id}><div
              className="catalog-ui-target"
              data-target-id={target.id}
            >
              {overview ? <h3 className="curation-target-sheet__group">
                <Button type="button" emphasis="quiet" className="curation-target-sheet__group-title" onClick={() => setSheetTargetId(target.id)}>{criteriaView.criteria[target.id]?.subject.label ?? target.title}<ChevronRight size={15} aria-hidden="true" /></Button>
                <span className="curation-target-sheet__count">{l("{count} candidates", "후보 {count}", { count: totalPoolCount })}</span><TargetBudgetLabel targetId={target.id} />
              </h3> : <>
              <p className="curation-target-sheet__context"><ProductVerticalBadge vertical={target.productVertical} /><span className="curation-target-sheet__count">{l("{count}/50 candidates", "후보 {count}/50", { count: totalPoolCount })}</span><TargetBudgetLabel targetId={target.id} /></p>
              <CriteriaEditor criteria={criteriaView.criteria[target.id]} curationId={response.curation.id} targetId={target.id} version={response.curation.version} onSave={value => { criteriaView.set(target.id, value); void onWorkChanged?.(); }} />
              {!response.conversation && <CurationSourceNotices coverage={storedPool?.sourceCoverage ?? []} scopeId={noticeScope} />}
              </>}


              <div
                id={targetViewId}
                className="catalog-ui-target__view"
              >
              {livePool && (catalogHydrationErrors[target.id] || livePool.products.some(
                (product) => catalogProductHydrationStatus(product) === "LOADING",
              )) ? catalogHydrationErrors[target.id] && !hydratingVisibleTargets[target.id] ? (
                <CurationDismissibleNotice
                  noticeId={JSON.stringify([noticeScope, "hydration", catalogHydrationAttempt, catalogHydrationErrors[target.id]])}
                  tone="warning">
                  <p>{catalogRateLimitReasons.has(catalogHydrationErrors[target.id])
                    ? l("Product detail requests are temporarily limited. Your saved recommendations are unchanged.", "현재 상품 정보 요청이 일시적으로 제한되고 있습니다. 저장된 추천 상품은 그대로입니다.")
                    : l("We couldn't refresh the product details. Your saved recommendations are unchanged.", "상품 정보를 갱신하지 못했습니다. 저장된 추천 상품은 그대로입니다.")}</p>
                  <Button type="button" size="compact" emphasis="quiet" disabled={busy}
                    onClick={() => setCatalogHydrationAttempt((attempt) => attempt + 1)}>
                    {l("Try loading details again", "상품 정보 다시 불러오기")}
                  </Button>
                </CurationDismissibleNotice>
              ) : (
                <p className="catalog-ui-target__enrichment-state" role="status">
                  {hydratingVisibleTargets[target.id]
                    ? <><LoaderCircle className="catalog-ui-spin" aria-hidden="true" /> {l("Loading product details", "상품 정보를 불러오는 중")}</>
                    : l("Saved product details will update shortly.", "저장된 상품 정보는 잠시 후 갱신됩니다.")}
                </p>
              ) : null}


              {workStatus.kind === "FAILED" ? (response.conversation ? null : (
                <div className="catalog-ui-target__work-status is-failed">
                  <CurationDismissibleNotice noticeId={JSON.stringify([noticeScope, workStatus.jobId, workStatus.reasonLabel])} tone="danger" title={l("We couldn't complete the research", "조사를 완료하지 못했습니다")}>
                    <p>{workStatus.reasonLabel}</p>
                    <p>
                      {l(
                        "Your existing recommendations are preserved. Use Research again to discover more products.",
                        "기존 추천 상품은 유지됩니다. 재조사로 상품을 더 찾아보세요.",
                      )}
                    </p>
                    {retryFailure && retryFailure.jobId === workStatus.jobId ? (
                      <p>{retryFailure.message}</p>
                    ) : null}
                    {workStatus.retryable && workStatus.jobId ? (
                      <Button
                        emphasis="quiet"
                        size="compact"
                        type="button"
                        busy={retryingJobId === workStatus.jobId}
                        onClick={() => retryTargetResearch(workStatus.jobId!)}
                      >
                        {l("Try again", "다시 시도")}
                      </Button>
                    ) : null}
                  </CurationDismissibleNotice>
                </div>
              )) : workStatus.kind !== "IDLE" && !(workStatus.kind === "NO_RESULTS" && storedPool?.discoveryOutcome) ? (
                <p className="catalog-ui-target__work-status" role="status">
                  {workStatus.kind === "QUEUED" ? (
                    workStatus.reason === "MODEL_SLOT_BUSY"
                      ? l("Research intelligence is busy. This product starts shortly.", "조사 지능이 바빠 잠시 뒤 시작합니다.")
                      : workStatus.reason === "RETRY_BACKOFF"
                        ? l("Research will be retried shortly.", "잠시 뒤 조사를 다시 시도합니다.")
                        : workStatus.reason
                          ? l("Product sources are busy with another research. This product starts shortly.", "상품 소스를 다른 조사가 쓰고 있어 잠시 뒤 시작합니다.")
                          : workStatus.ahead
                            ? l("Waiting for research · {count} ahead", "조사 대기 중 · 앞에 {count}개", { count: workStatus.ahead })
                            : l("Waiting for research.", "조사를 기다리고 있습니다.")
                  ) : workStatus.kind === "NO_RESULTS" ? (
                    l(
                      "No products matched the research country and conditions. Adjust the conditions and research again.",
                      "조사 국가와 조건에 맞는 상품을 찾지 못했습니다. 조건을 조정해 재조사해 보세요.",
                    )
                  ) : (
                    <>
                      <LoaderCircle className="catalog-ui-spin" aria-hidden="true" />{" "}
                      {workStatus.kind === "SYNC_RUNNING"
                        ? l("Vitlane is finding recommendations for this product.", "Vitlane이 이 상품의 추천 상품을 찾고 있습니다.")
                        : storedPool?.discoveryOutcome?.status === "EVALUATING"
                          ? l("Found {count} products · scoring them now.", "상품 {count}개를 확인했습니다 · 평가 중입니다.", { count: storedPool.discoveryOutcome.addedCount })
                          : l("Vitlane is researching this product.", "Vitlane이 이 상품을 조사하고 있습니다.")}
                    </>
                  )}
                </p>
              ) : null}

              {workStatus.kind === "IDLE" && (storedPool?.discoveryOutcome?.unevaluatedCount ?? 0) > 0 ? (
                <p className="catalog-ui-target__work-status" role="status">
                  {l(
                    "{unevaluated} of {added} new products from the last research are not scored yet. They stay in the list and sort as 0 until scored.",
                    "지난 조사의 신규 상품 {added}개 중 {unevaluated}개는 아직 평가되지 않았습니다. 목록에 남아 있으며 평가 전까지 정렬 시 0점으로 둡니다.",
                    { unevaluated: storedPool!.discoveryOutcome!.unevaluatedCount!, added: storedPool!.discoveryOutcome!.addedCount },
                  )}
                </p>
              ) : null}

              {!overview && visibleCount === 0 && hiddenToggle}
              {visibleCount === 0 ? (
                workStatus.kind === "IDLE" ? (
                  <p className="catalog-ui-target__empty">
                    {l(
                      "Use Research again to find recommendations.",
                      "재조사로 추천 상품을 더 찾아보세요.",
                    )}
                  </p>
                ) : null
              ) : (
                <TargetComparison priceOf={candidatePrice} products={liveProducts} cohort={uniqueProducts(livePool?.products ?? [])} criteria={criteriaView.criteria[target.id]}
                  combinationCandidateId={recommendation?.plan.items?.find(item=>item.targetId===target.id)?.candidateId} sort={targetViewState.views[target.id]?.sort ?? (recommendation?.plan.items?.some(item=>item.targetId===target.id) ? "COMBINATION" : "PICK")} onSortChange={sort => targetViewState.chooseSort(target.id, sort)} controls={hiddenToggle} bare={overview}>{sortedProducts => <CandidateGrid
                  targetTitle={target.title}
                >
                  {sortedProducts.map((product) => {
                    if (isKoreanExternalSource(product.source)) return <ExternalProductCandidate key={product.candidateId} product={product} curationId={response.curation.id} cardState={externalProductCardState(cardState, product.candidateId, product.externalObservation?.productRef)} onStateStale={onWorkChanged} onOpened={() => productOpened(target.id, product.candidateId)} />;
                    if (product.source === "AMAZON") {
                      const savedConfiguration = configuredCandidates[product.candidateId];
                      return <AmazonCandidate key={`amazon:${product.candidateId}`} product={product} curationId={response.curation.id} targetId={target.id} targetTitle={target.title} interactions={variantInteractions} signals={candidateSignals[product.candidateId]} configuredVariant={savedConfiguration?.variant} cardState={amazonCardState(cardState, product.sourceProductRef, savedConfiguration ? { variantId: savedConfiguration.variant.variantId, version: savedConfiguration.version } : undefined)} onStateStale={onWorkChanged} onInteraction={saveVariantReaction} onOpened={() => productOpened(target.id, product.candidateId)} />;
                    }
                    const card = shopifyCard(product, target, livePool);
                    return (
                      <div
                        key={`live:${product.candidateId}`}
                        className="catalog-ui-candidate-entry"
                        data-candidate-id={product.candidateId}
                      >
                        <CurationCandidateCard
                          evaluating={workStatus.kind === "RUNNING" || workStatus.kind === "QUEUED" || workStatus.kind === "SYNC_RUNNING"}
                          candidate={card.candidate}
                          state={card.state}
                          signals={candidateSignals[product.candidateId]}
                          onOpen={card.onOpen}
                          primaryAction={card.primaryAction}
                          statusMessage={card.statusMessage}
                        />
                      </div>
                    );
                  })}
                </CandidateGrid>}</TargetComparison>
              )}
              </div>
              <ShopifySearchPlatform messages={catalogGlobalDisplayMessages(livePool?.messages ?? [])} subjectLabel={target.title} />
              {!overview && <footer className="curation-target-sheet__foot">
                {targetFacts ? <ComparisonFactsLine facts={targetFacts} /> : null}
                <Button type="button" size="compact" emphasis="quiet" className="curation-target-sheet__remove" disabled={busy || Boolean(threads?.busy)}
                  aria-label={l("Remove product {title}", "{title} 상품 제거", { title: target.title })} onClick={() => setTargetRemoval(target)}>{l("Remove product group", "상품군 빼기")}</Button>
              </footer>}
            </div></BudgetTargetContext.Provider>
          );
  };
  // A product named in a reply, or shown as a representative, opens its details directly — never through
  // the product group's list. A Shopify product's details belong to this surface; an Amazon or Korean-mall
  // product's details belong to its card, so that card is mounted in details-only mode for the visit.
  const openCandidate = (candidateId: string, targetId: string) => {
    const target = targetViews.find(t => t.id === targetId) ?? targetViews.find(t => [...(livePools[t.id]?.products ?? []), ...(livePools[t.id]?.hiddenProducts ?? [])].some(p => p.candidateId === candidateId));
    if (!target) return;
    const pool = livePools[target.id];
    const product = [...(pool?.products ?? []), ...(pool?.hiddenProducts ?? [])].find(p => p.candidateId === candidateId);
    if (!product) return;
    if (product.source && product.source !== "SHOPIFY") { setDirectDetail({ candidateId, targetId: target.id, nonce: Date.now() }); return; }
    const open = shopifyCard(product, target, pool).onOpen;
    // A Shopify product whose details have not loaded yet has nothing to open; its card in the list says why.
    if (open) open(); else setSheetTargetId(target.id);
  };
  const openCandidateRef = useRef(openCandidate); openCandidateRef.current = openCandidate;
  const bindOpenCandidate = results?.bindOpenCandidate;
  useEffect(() => bindOpenCandidate?.((candidateId, targetId) => openCandidateRef.current(candidateId, targetId)), [bindOpenCandidate]);
  // A reply names a Shopify product by a reference the Server could not title (it saves no display facts of
  // that catalog); the name this surface just read for the card is the one the reply shows.
  const publishTitles = results?.publishTitles;
  useEffect(() => {
    if (!publishTitles) return;
    const titles: Record<string, string> = {};
    for (const pool of Object.values(livePools)) for (const product of [...(pool?.products ?? []), ...(pool?.hiddenProducts ?? [])]) if (product.title) titles[product.candidateId] = product.title;
    publishTitles(titles);
  }, [livePools, publishTitles]);

  const targetIds = targetViews.map(t => t.id);
  const requestThreads = threads?.threads ?? [];
  // Each request turn shows the product groups it worked on, from the moment it knows them (ADR-0089).
  const resultGroups = turnResultGroups(requestThreads, targetIds, Object.fromEntries(targetViews.map(t => [t.id, t.createdAt])));
  // The last research that added candidates is one time for every row of a product group, whichever turn shows it.
  const lastResearch = liveResultHolders(requestThreads, targetIds);
  const poolCount = (id: string) => uniqueCandidateCount(livePools[id]?.products ?? [], livePools[id]?.hiddenProducts ?? []);
  const externalReactions = new Map((response.catalogResearch?.productReactions ?? []).map(r => [r.candidateId, r]));
  // A product the reader hid or disliked, or whose details never resolved, does not stand for its product group.
  const canRepresent = (product: LiveCatalogProduct) => !candidateSignals[product.candidateId]?.disliked && externalReactions.get(product.candidateId)?.sentiment !== "DISLIKE"
    && product.hydration?.status !== "FAILED" && product.hydration?.status !== "UNRESOLVED";
  // "All" needs more than one product group to mean anything; with one group it is that group's list.
  const sheetOverview = sheetTargetId === allTargetsTab && targetViews.length > 1;
  const sheetTarget = sheetOverview ? undefined : targetViews.find(t => t.id === sheetTargetId) ?? (sheetTargetId === allTargetsTab ? targetViews[0] : undefined);
  const latestCombination = [...requestThreads].filter(thread => thread.status === "SUCCEEDED" && threadResponse(thread)?.combination)
    .sort((a,b) => Date.parse(b.createdAt)-Date.parse(a.createdAt))[0];
  const recommendation = targetViews.length > 1 && latestCombination ? {threadId:latestCombination.id,plan:threadResponse(latestCombination)!.combination!} : undefined;
  const combinationProducts = new Map(Object.values(livePools).flatMap(pool => pool.products).map(product => [product.candidateId,product]));
  const resultBlocks = resultGroups.map(group => {
    const holder = requestThreads.find(t => t.id === group.holderId);
    const holderRunning = holder ? threadActive(holder) : false;
    const layout = group.targetIds.length === 1 ? "card" as const : "row" as const;
    const node = <div className="curation-results" data-results-holder={group.holderId ?? ""}>
      <ul className="curation-results__list" data-layout={layout}>{group.targetIds.map(id => {
        const target = targetViews.find(t => t.id === id)!;
        const pool = livePools[id];
        const cohort = uniqueProducts(pool?.products ?? []).filter(canRepresent);
        const work = resolveTargetWorkStatus({ targetId: id, syncWorkingTargetId: liveWorkingTarget, jobs: intelligenceJobs, roundTargetIndex, latestRound: latestRoundForTarget(response.research.groups, id) });
        // A turn that is still working says so until its research reports otherwise.
        const status = work.kind === "FAILED" ? l("Research could not finish", "조사를 완료하지 못했어요") : holderRunning || work.kind === "RUNNING" || work.kind === "QUEUED" || work.kind === "SYNC_RUNNING" ? l("Researching", "조사 중") : l("No candidates yet", "아직 후보가 없어요");
        const view = targetViewState.views[id];
        return <BudgetTargetContext.Provider key={id} value={id}><ResultTarget priceOf={candidatePrice} layout={layout} targetId={id} title={criteriaView.criteria[id]?.subject.label ?? target.title}
          products={uniqueProducts(pool?.products ?? []).filter(canRepresent)} cohort={cohort} count={poolCount(id)} criteria={criteriaView.criteria[id]}
          combinationCandidateId={recommendation?.plan.items?.find(item=>item.targetId===id)?.candidateId}
          onRepresentative={rememberRepresentative}
          sort={view?.sort ?? (recommendation?.plan.items?.some(item=>item.targetId===id) ? "COMBINATION" : "PICK")} signal={view?.signal} researchedAt={requestThreads.find(t => t.id === lastResearch[id])?.updatedAt}
          inCart={candidateId => cartItems.some(item => item.candidateId === candidateId)}
          cartAction={product => !product.source || product.source === "SHOPIFY" ? shopifyCard(product, target, pool).primaryAction : undefined}
          // The row states what the product's card states: a Shopify product's price is its chosen option's when one is
          // saved and the lowest it was seen at otherwise, and a product Shopify has not been asked about yet is loading.
          display={product => { if (product.source && product.source !== "SHOPIFY") return undefined; const card = shopifyCard(product, target, pool); return { price: card.candidate.price, loading: card.state === "loading" }; }}
          onOpenCandidate={product => openCandidate(product.candidateId, id)} onOpenSheet={() => setSheetTargetId(id)} status={status} /></BudgetTargetContext.Provider>;
      })}</ul>
      {targetViews.length > 1 && <CombinationCartAction curationId={response.curation.id} representatives={targetIds.flatMap(targetId=>representatives[targetId]?[{targetId,candidateId:representatives[targetId]!}]:[])} products={combinationProducts}
        configurations={configuredCandidates} cart={cartItems} cartVersion={cartVersion} disabled={Boolean(threads?.busy) || cartSaving || cartLoadState !== "READY"}
        onBusy={setCartSaving} onSaved={cart => {setCartItems(cart.items);setCartVersion(cart.version);setCartMarket({country:cart.country,currency:cart.currency});setPreparation(undefined);setDraftError(undefined);}}
        onChooseOption={(candidateId, targetId) => openCandidate(candidateId, targetId)} />}
      {/* Under the last row, on the right: every product group's candidates at once (the sheet's first tab). */}
      {targetViews.length > 1 && <Button type="button" emphasis="quiet" size="compact" className="curation-results__all" aria-haspopup="dialog" onClick={() => setSheetTargetId(allTargetsTab)}>{l("View all candidates", "전체 후보 보기")}</Button>}
      {/* A store's required notice stays in view beside the product it is about. */}
      <div className="curation-results__notices">{group.targetIds.map(id => <ShopifySearchPlatform key={id} messages={catalogGlobalDisplayMessages(livePools[id]?.messages ?? [])} subjectLabel={targetViews.find(t => t.id === id)?.title ?? ""} />)}</div>
    </div>;
    // A turn's rows appear only inside that turn; until its place is ready they are not drawn anywhere else.
    if (group.holderId) {
      const host = results?.hosts[group.holderId];
      return host ? createPortal(node, host, group.holderId) : null;
    }
    return <Fragment key="unheld">{node}</Fragment>;
  });

  return (
    <ResearchCurrencyProvider curationId={response.curation.id}><BudgetProvider key={response.curation.id} curationId={response.curation.id} targets={response.targets}><div className="catalog-ui-curation" data-testid="phase8-curation-surface">
      <div
        className="catalog-ui-curation__targets"
        data-target-count={targetViews.length}
      >
        <ShopifySearchPlatform
          messages={catalogGlobalDisplayMessages(workspaceMessages)}
          subjectLabel={l("Current research", "현재 조사")}
        />
        <div className="catalog-ui-curation__unheld">{resultBlocks}</div>
      </div>

      {targetViews.length === 0 ? (
        <div className="catalog-ui-curation__empty">
          <strong>{l("There are no products to research yet.", "아직 조사할 상품이 없습니다.")}</strong>
          <p>{l("Click + below and describe the product you want to add.", "아래 +를 누르고 추가할 상품을 설명해 주세요.")}</p>
        </div>
      ) : null}

      {!cartOpen && draftError ? (
        <CurationDismissibleNotice noticeId={JSON.stringify([response.curation.id, "cart", draftError])} tone="danger" title={l("Review cart saving", "장바구니 저장을 확인해 주세요")}>
          {draftError}
        </CurationDismissibleNotice>
      ) : null}

      {(!threadOwnsActivity && activitySlot.kind !== "EMPTY") || conversationTail || activitySlot.kind === "LOCAL_PROCESSING" ? (
        <div
          className="catalog-ui-conversation-latest"
          data-testid="phase8-latest-slot"
          aria-label={l("Current action and progress", "현재 행동과 진행 상태")}
        >
          {conversationTail}
          {activitySlot.kind === "LOCAL_PROCESSING" ? (
            <section
              className="catalog-ui-local-activity is-running"
              role="status"
            >
              <span aria-hidden="true">
                <LoaderCircle className="catalog-ui-spin" />
              </span>
              <div>
                <strong>{activitySlot.activity.label}</strong>
              </div>
            </section>
          ) : !threadOwnsActivity && activitySlot.kind === "RESULT_CONFIRMATION_REQUIRED" ? (
            <CurationResultConfirmationPanel
              job={activitySlot.job}
              onWorkChanged={onWorkChanged}
            />
          ) : !threadOwnsActivity && activitySlot.kind === "SERVER_PROCESSING" ? (
            activitySlot.job ? (
              <CurationAgentRunningPanel
                job={activitySlot.job}
                activeWork={response.activeWork}
                onWorkChanged={onWorkChanged}
              />
            ) : (
              <section
                className="catalog-ui-local-activity is-running"
                role="status"
              >
                <span aria-hidden="true">
                  <LoaderCircle className="catalog-ui-spin" />
                </span>
                <div>
                  <strong>{response.activeWork?.label ?? l("Vitlane job", "Vitlane 작업")}</strong>
                </div>
              </section>
            )
          ) : null}
        </div>
      ) : null}
      <CurationComposerDock>
        {/* While the cart loads, the cart button says so (`Cart …`) and assistive technology hears why the
            cart actions wait; nothing is drawn in the dock, so the composer never moves (ADR-0089). A cart that
            could not be verified asks for the reader's action and stands above the composer. */}
        {cartLoadState === "LOADING" ? (
          <p className="vt-visually-hidden" role="status" aria-live="polite">
            {l("Synchronizing the cart with the server.", "장바구니를 서버와 동기화하고 있습니다.")}{" "}
            {l("Adding, changing, and ordering are briefly paused while we confirm the latest items.", "최신 상품을 확인하는 동안 담기·변경·주문이 잠시 중단됩니다.")}
          </p>
        ) : cartLoadState === "FAILED" ? (
          <section className="catalog-ui-cart-sync is-failed" role="alert" aria-live="polite">
            <span aria-hidden="true">!</span>
            <div>
              <strong>{l("We couldn't verify the cart state.", "장바구니 상태를 확인하지 못했습니다.")}</strong>
              <span>{l("Reload the cart before adding, changing, or ordering items.", "상품을 담거나 변경하거나 주문하기 전에 장바구니를 다시 불러와 주세요.")}</span>
            </div>
            <Button
              type="button"
              size="compact"
              emphasis="secondary"
              onClick={() => setCartLoadAttempt((current) => current + 1)}
            >
              {l("Reload cart", "장바구니 다시 불러오기")}
            </Button>
          </section>
        ) : null}
        <CurationThreadProgress />
        <CurationComposer contextSettings={<BudgetBar settings={<ResearchSettings key={response.curation.id} curationId={response.curation.id} compact />} action={
            <Button
              type="button"
              className="catalog-ui-draft-trigger"
              emphasis="quiet"
              aria-label={cartLoadState === "LOADING" ? l("Cart …", "장바구니 …") : cartLoadState === "FAILED" ? l("Cart needs review", "장바구니 확인 필요") : l("Cart ({count})", "장바구니 ({count})", { count: cartItems.length })}
              onClick={() => setCartOpen(true)}
              disabled={cartLoadState !== "FAILED" && cartItems.length === 0}
            >
              <ShoppingBag size={15} aria-hidden="true" />
              <span className="catalog-ui-draft-trigger__label">{cartLoadState === "LOADING"
                ? l("Cart …", "장바구니 …")
                : cartLoadState === "FAILED"
                  ? l("Cart needs review", "장바구니 확인 필요")
                  : (
                    l("Cart", "장바구니")
                  )}</span>
              {cartLoadState === "READY" && <> <span className="catalog-ui-draft-trigger__count">{cartItems.length}</span></>}
            </Button>
          } />} country={country} focus={focus} selectFocus={selectFocus} busy={busy || Boolean(threads?.busy)}
          modeSelectorOpen={modeSelectorOpen} setModeSelectorOpen={setModeSelectorOpen}
          autoResolutionMessage={autoResolutionMessage} researchTargets={response.targets.map(t=>({...t,title:criteriaView.criteria[t.id]?.subject.label ?? t.title}))}
          retryJobs={(response.intelligence ?? []).filter(j=>j.status==="FAILED"&&j.retryable).map(j=>({jobId:j.jobId,title:response.targets.find(t=>response.research.groups.some(g=>g.session.planTargetId===t.id&&g.round?.id===j.targetId))?.title ?? l("Research request","조사 요청")}))}
          unavailableTargets={(response.catalogResearch?.pools ?? []).filter(p=>p.products.length+p.hiddenProducts.length>=50).map(p=>p.targetId)}
          composerRef={composerRef} instruction={instruction} setInstruction={setInstruction}
          submitComposer={submitComposer}
        />
      </CurationComposerDock>

      {cartOpen ? (
        <CartViewDrawer
          items={cartItems}
          country={cartMarket.country}
          currency={cartMarket.currency}
          busy={preparing || cartSaving || cartLoadState === "LOADING"}
          cartLoadState={cartLoadState}
          error={draftError}
          preparation={preparation}
          orderCapability={orderCapability}
          onClose={() => setCartOpen(false)}
          onRetryCart={() => setCartLoadAttempt((current) => current + 1)}
          onPrepare={() => void prepareOrder()}
          onEdit={(item) => {
            setEditingCartItemID(item.cartItemId);
            setCandidateModal(cartItemCandidateView(item, l));
          }}
          onRemove={(cartItemId) => {
            void replaceCart(
              cartItems.filter((item) => item.cartItemId !== cartItemId),
              l("Remove from cart", "장바구니에서 제거"),
            ).catch(() => undefined);
          }}
          onQuantity={(cartItemId, quantity) => {
            void replaceCart(
              cartItems.map((item) =>
                item.cartItemId === cartItemId ? { ...item, quantity } : item,
              ),
              l("Change cart quantity", "장바구니 수량 변경"),
            ).catch(() => undefined);
          }}
        />
      ) : null}
      {candidateModal ? (
        <BudgetTargetContext.Provider value={candidateModal.targetId}><CatalogCandidateModal
          candidate={candidateModal}
          country={cartMarket.country}
          currency={cartMarket.currency}
          cartItems={cartItems}
          cartReady={cartLoadState === "READY"}
          cartLoadFailed={cartLoadState === "FAILED"}
          cartEditMode={Boolean(editingCartItemID)}
          interactions={variantInteractions}
          onAdd={addCartItem}
          onRetryCart={() => setCartLoadAttempt((current) => current + 1)}
          onSaveOption={saveCandidateOption}
          onClose={() => {
            setCandidateModal(undefined);
            setEditingCartItemID(undefined);
          }}
          onInteraction={saveVariantReaction}
        /></BudgetTargetContext.Provider>
      ) : null}
      {(() => {
        // A product opened straight from the conversation. Its own card component owns the details
        // (options, purchase check, reactions), so it is mounted for the visit without drawing the card.
        const target = directDetail && targetViews.find(t => t.id === directDetail.targetId);
        const pool = target && livePools[target.id];
        const product = pool && [...pool.products, ...pool.hiddenProducts].find(p => p.candidateId === directDetail!.candidateId);
        if (!directDetail || !target || !product) return null;
        const close = () => setDirectDetail(current => current?.nonce === directDetail.nonce ? undefined : current);
        const opened = () => targetViewState.recordViewed(target.id, product.candidateId);
        return <BudgetTargetContext.Provider value={target.id}>{isKoreanExternalSource(product.source)
          ? <ExternalProductCandidate key={`direct:${directDetail.nonce}`} detailsOnly product={product} curationId={response.curation.id} cardState={externalProductCardState(cardState, product.candidateId, product.externalObservation?.productRef)} onStateStale={onWorkChanged} openRequest={directDetail.nonce} onOpened={opened} onClosed={close} />
          : <AmazonCandidate key={`direct:${directDetail.nonce}`} detailsOnly product={product} curationId={response.curation.id} targetId={target.id} targetTitle={target.title} interactions={variantInteractions} signals={candidateSignals[product.candidateId]} configuredVariant={configuredCandidates[product.candidateId]?.variant}
              cardState={amazonCardState(cardState, product.sourceProductRef, configuredCandidates[product.candidateId] ? { variantId: configuredCandidates[product.candidateId].variant.variantId, version: configuredCandidates[product.candidateId].version } : undefined)} onStateStale={onWorkChanged} onInteraction={saveVariantReaction} openRequest={directDetail.nonce} onOpened={opened} onClosed={close} />}</BudgetTargetContext.Provider>;
      })()}
      {sheetTarget || sheetOverview ? (
        <TargetSheet
          // The tab row is the index of every candidate: "All" first, then one tab per product group.
          tabs={[...(targetViews.length > 1 ? [{ id: allTargetsTab, title: l("All", "전체"), count: targetViews.reduce((sum, t) => sum + poolCount(t.id), 0) }] : []),
            ...targetViews.map(t => ({ id: t.id, title: criteriaView.criteria[t.id]?.subject.label ?? t.title, count: poolCount(t.id) }))]}
          activeId={sheetTarget ? sheetTarget.id : allTargetsTab}
          onSelect={setSheetTargetId}
          onClose={() => setSheetTargetId(undefined)}
          title={sheetTarget ? criteriaView.criteria[sheetTarget.id]?.subject.label ?? sheetTarget.title : l("All candidates", "전체 후보")}
        >
          {sheetOverview && recommendation && <section className="curation-recommended-combination" aria-label={l("Recommended combination","추천 조합")}>
            <h3 className="curation-target-sheet__group">{l("Recommended combination","추천 조합")}</h3>
            <ul className="curation-results__list" data-layout="row">
              {(recommendation.plan.items??[]).map(item=>{
                const target=targetViews.find(t=>t.id===item.targetId),product=combinationProducts.get(item.candidateId);
                if(!target||!product||!canRepresent(product))return null;
                return <BudgetTargetContext.Provider key={item.targetId} value={item.targetId}>
                  <ResultTarget layout="row" targetId={item.targetId} title={target.title} products={[product]} cohort={[product]} count={poolCount(item.targetId)}
                    sort="COMBINATION" combinationCandidateId={item.candidateId} inCart={()=>false} cartAction={()=>undefined}
                    priceOf={candidatePrice} display={p=>({price:candidatePrice(p),loading:false})}
                    onOpenCandidate={()=>openCandidate(item.candidateId,item.targetId)} onOpenSheet={()=>setSheetTargetId(item.targetId)} status="" />
                </BudgetTargetContext.Provider>;
              })}
            </ul>
          </section>}
          {sheetTarget ? targetSheetBody(sheetTarget) : targetViews.map(target => targetSheetBody(target, true))}
        </TargetSheet>
      ) : null}
      {targetRemoval ? (
        <TargetRemovalDialog
          target={targetRemoval}
          busy={busy}
          onClose={() => setTargetRemoval(undefined)}
          onConfirm={async () => {
            const title = targetRemoval.title;
            setLocalActivity({
              label: l("Remove product · {title}", "상품 제거 · {title}", { title }),
              detail: l("Removing this product from your research.", "조사 목록에서 이 상품을 제거하고 있습니다."),
            });
            try {
              await onRemoveTarget(targetRemoval.id);
              setTargetRemoval(undefined);
              pushMessage({
                role: "VITLANE",
                title: l("Remove product · {title}", "상품 제거 · {title}", { title }),
                body: l("Removed the product from this research.", "조사 목록에서 상품을 제거했습니다."),
              });
            } catch (caught) {
              pushMessage({
                role: "VITLANE",
                title: l("We couldn't remove {title}", "{title}을 제거하지 못했습니다", { title }),
                body: l("The product and its recommendations are unchanged. Try again.", "상품과 추천 상품은 그대로 유지됩니다. 다시 시도해 주세요."),
              });
              throw caught;
            } finally {
              setLocalActivity(undefined);
            }
          }}
        />
      ) : null}
    </div></BudgetProvider></ResearchCurrencyProvider>
  );
}


function CartViewDrawer({
  items,
  country,
  currency,
  busy,
  cartLoadState,
  error,
  preparation,
  orderCapability,
  onClose,
  onRetryCart,
  onPrepare,
  onEdit,
  onRemove,
  onQuantity,
}: {
  items: LiveCartItem[];
  country: string;
  currency: string;
  busy: boolean;
  cartLoadState: "LOADING" | "READY" | "FAILED";
  error?: string;
  preparation?: AgencyOrderPreparationPreview;
  orderCapability: "CHECKING" | "READY" | "UNAVAILABLE";
  onClose: () => void;
  onRetryCart: () => void;
  onPrepare: () => void;
  onEdit: (item: LiveCartItem) => void;
  onRemove: (cartItemId: string) => void;
  onQuantity: (cartItemId: string, quantity: number) => void;
}) {
  const { l } = useLocale();
  const drawerRef = useRef<HTMLElement>(null);
  const backdropRef = useRef<HTMLDivElement>(null);
  // The drawer enters from the right edge; a touch drag back to it closes the cart.
  useSwipeDismiss(drawerRef, { direction: "right", backdropRef, onDismiss: onClose });
  return (
    <div
      ref={backdropRef}
      className="catalog-ui-draft-backdrop vt-scrim"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <aside
        ref={drawerRef}
        className="catalog-ui-draft-drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby="phase8-cart-title"
      >
        <header>
          <div>
            <span>{l("Shipping country · {country}", "배송 국가 · {country}", { country })}</span>
            <h2 id="phase8-cart-title">{l("Cart", "장바구니")}</h2>
            <p>{l("Keep your selections here. Price and availability are confirmed before checkout.", "선택한 상품을 담아두세요. 가격과 구매 가능 여부는 결제 전에 확인합니다.")}</p>
          </div>
          <Button type="button" emphasis="quiet" aria-label={l("Close cart", "장바구니 닫기")} onClick={onClose}>
            <X aria-hidden="true" />
          </Button>
        </header>
        <div className="catalog-ui-draft-drawer__body">
          {cartLoadState === "READY" && <CartBudgetView items={items} />}
          {cartLoadState === "FAILED" ? (
            <div className="catalog-ui-draft-error" role="alert">
              <p>{l("We couldn't confirm your cart. Reload it before making changes or ordering.", "장바구니를 확인하지 못했습니다. 변경하거나 주문하기 전에 다시 불러와 주세요.")}</p>
              <Button type="button" size="compact" emphasis="secondary" onClick={onRetryCart}>
                {l("Reload cart", "장바구니 다시 불러오기")}
              </Button>
            </div>
          ) : null}
          <ul>
            {items.map((item) => {
              const prepared = preparation?.lines.find(
                (line) => line.cartItemId === item.cartItemId,
              );
              return (
                <li key={item.cartItemId}>
                  <div>
                    <span>{item.variantTitle || l("Default variant", "Default variant")}</span>
                    <strong>{item.productTitle}</strong>
                    <small>{item.selectedOptions.join(" · ") || item.variantId}</small>
                  </div>
                  <strong>
                    {formatMinor(item.previewPriceMinor, item.previewCurrency)}
                  </strong>
                  <label>
                    <span>{l("Quantity", "수량")}</span>
                    <NativeSelect
                      value={item.quantity}
                      disabled={busy || cartLoadState !== "READY"}
                      onChange={(event) =>
                        onQuantity(item.cartItemId, Number(event.target.value))
                      }
                    >
                      {[1, 2, 3, 4, 5].map((quantity) => (
                        <NativeSelectOption key={quantity} value={quantity}>
                          {quantity}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </label>
                  <Button type="button" size="compact" emphasis="secondary" disabled={busy || cartLoadState !== "READY"} onClick={() => onEdit(item)}>
                    {l("Change option", "옵션 변경")}
                  </Button>
                  <Button type="button" size="compact" emphasis="quiet" disabled={busy || cartLoadState !== "READY"} onClick={() => onRemove(item.cartItemId)}>
                    {l("Remove", "제거")}
                  </Button>
                  {prepared ? (
                    <div className={`catalog-ui-draft-result is-${prepared.status.toLowerCase()}`}>
                      <strong>{prepared.status === "READY" ? l("Ready", "준비됨") : prepared.status === "PRICE_CHANGED" ? l("Price changed", "가격 변경") : prepared.status === "UNAVAILABLE" ? l("Unavailable", "구매 불가") : l("Review needed", "확인 필요")}</strong>
                      <span>
                        {l("Current price", "현재 가격")} {formatMinor(prepared.currentPriceMinor, prepared.currency)}
                        {prepared.priceChanged ? l(" · Price changed", " · 가격 변경") : l(" · Price unchanged", " · 가격 동일")}
                        {prepared.available ? l(" · In stock", " · 재고 확인") : l(" · Sold out or needs review", " · 품절/확인 필요")}
                      </span>
                      {prepared.productUrl ? (
                        <a href={prepared.productUrl} target="_blank" rel="noreferrer">
                          {l("Shopify product", "Shopify 상품")} <ExternalLink size={13} aria-hidden="true" />
                        </a>
                      ) : null}
                    </div>
                  ) : null}
                </li>
              );
            })}
          </ul>
          {error ? <p className="catalog-ui-draft-error" role="alert">{error}</p> : null}
          {preparation ? (
            <div className="catalog-ui-draft-summary">
              <span>{l("Estimated total", "예상 합계")}</span>
              <strong>{formatMinor(preparation.subtotalMinor, preparation.currency)}</strong>
            </div>
          ) : null}
        </div>
        <footer>
          <div>
            <span>{l("{count} items in your cart", "장바구니 상품 {count}개", { count: items.length })}</span>
            <strong>{l("Currency · {currency}", "통화 · {currency}", { currency })}</strong>
          </div>
          <Button
            type="button"
            emphasis="primary"
            busy={busy}
            disabled={busy || cartLoadState !== "READY" || items.length === 0 || orderCapability !== "READY"}
            onClick={onPrepare}
          >
				{orderCapability === "CHECKING" ? l("Checking ordering", "주문 기능 확인 중") : orderCapability === "UNAVAILABLE" ? l("Ordering not ready", "주문 기능 준비 중") : l("Place order", "주문하기")}
          </Button>
        </footer>
      </aside>
    </div>
  );
}

function TargetRemovalDialog({
  target,
  busy,
  onClose,
  onConfirm,
}: {
  target: PlanTarget;
  busy: boolean;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  const { l } = useLocale();
  // The confirmation can open from the product group's sheet, which makes the rest of the page inert;
  // it is its own layer in the body so it stays reachable and closes before the sheet does.
  const panel = useRef<HTMLElement>(null);
  useModalLayer(panel, onClose);
  return createPortal(
    <div
      className="catalog-ui-confirm-backdrop vt-scrim"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section
        ref={panel}
        className="catalog-ui-confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="phase8-target-remove-title"
        aria-describedby="phase8-target-remove-description"
      >
        <span>{l("Remove product", "상품 제거")}</span>
        <h2 id="phase8-target-remove-title">{l("Are you sure you want to remove this product?", "정말 이 상품을 제거하시겠습니까?")}</h2>
        <p id="phase8-target-remove-description">
          {l("The current recommendations for {title} will be removed from this page.", "{title}의 현재 추천 상품이 이 화면에서 사라집니다.", { title: target.title })}
        </p>
        <div>
          <Button type="button" emphasis="quiet" disabled={busy} onClick={onClose}>
            {l("Cancel", "취소")}
          </Button>
          <Button type="button" emphasis="danger" busy={busy} onClick={() => void onConfirm()}>
            {l("Remove", "제거")}
          </Button>
        </div>
      </section>
    </div>,
    document.body,
  );
}

function ShopifySearchPlatform({
  messages,
  subjectLabel,
}: {
  messages: LiveCatalogProviderMessage[];
  subjectLabel: string;
}) {
  const { l } = useLocale();
  if (messages.length === 0) return null;
  return (
    <aside
      className="catalog-ui-search-platform"
      aria-label={l("Shopify information for {subject}", "{subject} 관련 Shopify 안내", { subject: subjectLabel })}
    >
      <header className="catalog-ui-search-platform__identity">
        <span className="catalog-ui-search-platform__icon" aria-hidden="true">
          <ShoppingBag size={14} />
        </span>
        <strong>{l("Shopify", "Shopify")}</strong>
        <span>{l("Product information", "상품 안내")}</span>
      </header>
      {messages.length > 0 ? (
        <div className="catalog-ui-search-platform__messages">
          {messages.map((message, index) => {
            const presentation = catalogMessagePresentation(message);
            const imageURL = safeCatalogMessageURL(message.imageUrl);
            const linkURL = safeCatalogMessageURL(message.url);
            return (
              <article
                className={`catalog-ui-search-platform__message is-${presentation}`}
                data-content-type={message.contentType || "plain"}
                data-message-code={message.code}
                data-provider-path={message.path}
                data-severity={message.severity}
                key={catalogMessageKey(message, index)}
                role="note"
                aria-label={l("Shopify {presentation}", "Shopify {presentation}", { presentation })}
              >
                <span className="catalog-ui-search-platform__tag">
                  {presentation === "disclosure" ? l("Disclosure", "Disclosure") : l("Notice", "Notice")}
                </span>
                {imageURL ? (
                  <img
                    src={imageURL}
                    alt={l("Shopify information related to {subject}", "{subject} 관련 Shopify 안내 이미지", { subject: subjectLabel })}
                    loading="lazy"
                    decoding="async"
                    referrerPolicy="no-referrer"
                  />
                ) : null}
                <blockquote cite={linkURL}>
                  <p>{message.content}</p>
                </blockquote>
                {linkURL ? (
                  <a
                    href={linkURL}
                    target="_blank"
                    rel="nofollow noopener noreferrer"
                    aria-label={l("View detailed Shopify information for {subject} in a new window", "{subject} Shopify 안내 자세히 보기, 새 창", { subject: subjectLabel })}
                  >
                    {l("View details", "안내 자세히 보기")}
                    <ExternalLink size={13} aria-hidden="true" />
                  </a>
                ) : null}
              </article>
            );
          })}
        </div>
      ) : null}
    </aside>
  );
}

function catalogResearchWorkspace(
  response: CurationWorkspaceResponse,
): CatalogWorkspaceResponse {
  const workspace = response.catalogResearch;
  const emptyMetrics = {
    policyVersion: "catalog-research-workspace.v1",
    durationMilliseconds: 0,
    aiCallCount: 0,
    aiCostUsd: "0",
    shopifyCallCount: 0,
    providerCostStatus: "NOT_CALLED",
    providerBillingCredential: false,
    localCallsUsed: 0,
    localCallsRemaining: 0,
    localRateLimit: 0,
    localRateWindowSeconds: 0,
    externalEffect: "NONE",
  };
  if (!workspace) {
    return {
      schemaVersion: "vitlane.catalog-research-workspace.v1",
      pools: [], messages: [], configurations: [], interactions: [],
      metrics: emptyMetrics,
    };
  }
  const mapCandidate = (
    candidate: NonNullable<typeof workspace>["pools"][number]["products"][number],
  ): LiveCatalogProduct => ({
    candidateId: candidate.candidateId,source:candidate.source,sourceProductRef:candidate.sourceProductRef,
    title: candidate.externalObservation?.title ?? candidate.title ?? "",
    externalObservation: candidate.externalObservation,
    description: candidate.description ?? "",
    priceMinimumMinor: candidate.externalObservation ? candidate.externalObservation.price.amountMinor : candidate.priceMinimumMinor ?? 0,
    priceMaximumMinor: candidate.externalObservation ? candidate.externalObservation.price.amountMinor : candidate.priceMaximumMinor ?? 0,
    currency: candidate.externalObservation?.price.currency ?? candidate.currency ?? "",
    mediaUrl: candidate.externalObservation?.imageUrl ?? candidate.mediaUrl,
    mediaAlt: candidate.mediaAlt,
    categories: candidate.categories ?? [],
    locator: candidate.locator,
    previewVariant: candidate.previewVariant,
    intentPoint: candidate.intentPoint,
    axisAssessment: candidate.axisAssessment,
    features: candidate.features ?? [],
    specifications: candidate.specifications ?? [],
  });
  return {
    schemaVersion: workspace.schemaVersion,
    messages: [],
    pools: workspace.pools.map((pool) => ({
      targetId: pool.targetId,
      version: pool.version,
      expandOrdinal: pool.expandOrdinal,
      latestMode: pool.latestMode, sourceCoverage:pool.sourceCoverage, discoveryOutcome: pool.discoveryOutcome,
      latestDurationMilliseconds: pool.latestDurationMilliseconds ?? 0,
      latestShopifyCalls: pool.latestShopifyCalls ?? 0,
      latestRateRemaining: pool.latestRateRemaining ?? 0,
      products: pool.products.map(mapCandidate),
      hiddenProducts: pool.hiddenProducts.map(mapCandidate),
      messages: [],
    })),
    configurations: workspace.configurations,
    interactions: workspace.interactions,
    purchaseFeedback: workspace.purchaseFeedback,
    productReactions: workspace.productReactions,
    reactionAllowedCandidateIds: workspace.reactionAllowedCandidateIds,
    metrics: emptyMetrics,
  };
}

function workspacePools(
  workspace: CatalogWorkspaceResponse,
): Record<string, LivePool> {
  return Object.fromEntries(workspace.pools.map((pool) => [
    pool.targetId,
    {
      version: pool.version,
      products: pool.products,
      hiddenProducts: pool.hiddenProducts,
      messages: bindCatalogMessagesToProducts(pool.messages ?? [], [
        ...pool.products,
        ...pool.hiddenProducts,
      ]),
      latestMode: pool.latestMode ?? "APPEND",
      latestDurationMilliseconds: pool.latestDurationMilliseconds,
      latestShopifyCalls: pool.latestShopifyCalls,
      latestRateRemaining: pool.latestRateRemaining,
    },
  ]));
}

function mergeStoredWorkspacePools(
  current: Record<string, LivePool>,
  workspace: CatalogWorkspaceResponse,
): Record<string, LivePool> {
  const stored = workspacePools(workspace);
  return Object.fromEntries(Object.entries(stored).map(([targetID, pool]) => {
    const existing = current[targetID];
    const existingProducts = new Map(
      [...(existing?.products ?? []), ...(existing?.hiddenProducts ?? [])]
        .map((product) => [product.candidateId, product]),
    );
    const preserveFreshDisplay = (product: LiveCatalogProduct) => {
      if (product.externalObservation) return product;
      const fresh = existingProducts.get(product.candidateId);
      if (!fresh?.title && !fresh?.hydration) return product;
      return {
        ...product,
        ...fresh,
        locator: product.locator ?? fresh.locator,
        intentPoint: product.intentPoint || fresh.intentPoint,
        axisAssessment: product.axisAssessment,
        features: (product.features ?? []).length > 0
          ? product.features
          : (fresh.features ?? []),
        specifications: (product.specifications ?? []).length > 0
          ? product.specifications
          : (fresh.specifications ?? []),
      };
    };
    return [targetID, {
      ...pool,
      products: pool.products.map(preserveFreshDisplay),
      hiddenProducts: pool.hiddenProducts.map(preserveFreshDisplay),
      messages: existing?.messages ?? pool.messages,
    }];
  }));
}

function bindCatalogMessagesToProducts(
  messages: LiveCatalogProviderMessage[],
  products: LiveCatalogProduct[],
): LiveCatalogProviderMessage[] {
  return messages.map((message) => {
    if (message.subjectRef || message.subjectKind?.toUpperCase() === "SEARCH") {
      return message;
    }
    const match = message.path?.trim().match(/^\$\.products\[(\d+)\](?:$|[.\[])/);
    if (!match) return message;
    const product = products[Number(match[1])];
    if (!product) return message;
    return {
      ...message,
      subjectKind: "PRODUCT",
      subjectRef: product.candidateId,
    };
  });
}

function catalogMessagePresentation(
  message: LiveCatalogProviderMessage,
): "notice" | "disclosure" {
  return message.presentation?.trim().toLowerCase() === "disclosure"
    ? "disclosure"
    : "notice";
}

function catalogMessageMustDisplay(message: LiveCatalogProviderMessage) {
  // UCP warning content and every disclosure are MUST-display. Omitted
  // presentation defaults to notice; an unknown warning extension is rendered
  // as the same non-dismissible notice instead of losing provider content.
  return message.type.trim().toLowerCase() === "warning" ||
    message.presentation?.trim().toLowerCase() === "disclosure";
}

function catalogProductDisplayMessages(
  messages: LiveCatalogProviderMessage[],
  productID: string,
) {
  return messages.filter(
    (message) => catalogMessageMustDisplay(message) &&
      message.subjectKind?.toUpperCase() === "PRODUCT" &&
      message.subjectRef === productID,
  );
}

function catalogGlobalDisplayMessages(messages: LiveCatalogProviderMessage[]) {
  return messages.filter(
    (message) => catalogMessageMustDisplay(message) &&
      message.subjectKind?.toUpperCase() !== "PRODUCT",
  );
}

function safeCatalogMessageURL(value?: string) {
  const trimmed = value?.trim();
  if (!trimmed) return undefined;
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "https:" || !parsed.hostname || parsed.username || parsed.password) {
      return undefined;
    }
    return trimmed;
  } catch {
    return undefined;
  }
}

function uniqueCatalogMessages(messages: LiveCatalogProviderMessage[]) {
  const seen = new Set<string>();
  return messages.filter((message, index) => {
    const key = catalogMessageKey(message, index, false);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function catalogMessageKey(
  message: LiveCatalogProviderMessage,
  index: number,
  includeIndex = true,
) {
  return [
    message.type,
    message.code,
    message.path,
    message.subjectKind,
    message.subjectRef,
    message.contentType,
    message.content,
    message.severity,
    message.presentation,
    message.imageUrl,
    message.url,
    includeIndex ? index : undefined,
  ].join("\u0000");
}

function uniqueProducts(products: LiveCatalogProduct[]) {
  const seen = new Set<string>();
  return products.filter((product) => {
    if (seen.has(product.candidateId)) return false;
    seen.add(product.candidateId);
    return true;
  });
}

function mergeFreshCatalogProducts(
  retained: LiveCatalogProduct[],
  fresh: LiveCatalogProduct[],
) {
  const freshByID = new Map(
    fresh.map((product) => [product.candidateId, product]),
  );
  return uniqueProducts([
    ...retained.map((product) =>
      freshByID.get(product.candidateId) ?? product
    ),
    ...fresh,
  ]);
}

function catalogProductHydrationStatus(
  product: LiveCatalogProduct,
): "LOADING" | "READY" | "UNRESOLVED" | "FAILED" {
  return product.hydration?.status ?? (product.title.trim() ? "READY" : "LOADING");
}

function liveCandidateView(
  product: LiveCatalogProduct,
  target: PlanTarget,
  curationID: string,
  configured?: SavedCandidateConfiguration,
  searchPlatformMessages: LiveCatalogProviderMessage[] = [],
  l: Localize = (english) => english,
): CatalogCandidateView {
  const catalogPreview = product.previewVariant;
  const preview = configured?.variant ?? (catalogPreview
    ? {
        variantId: catalogPreview.id,
        title: catalogPreview.title,
        priceMinor: catalogPreview.priceMinor,
        currency: catalogPreview.currency,
        available: Boolean(catalogPreview.available),
        selectedOptions:
          catalogPreview.title && catalogPreview.title !== "Default Title"
            ? [{ name: "Option", value: catalogPreview.title }]
            : [],
        mediaUrl: product.mediaUrl,
        productUrl: product.locator?.productUrl,
      }
    : undefined);
  return {
    curationId: curationID,
    targetId: target.id,
    candidateId: product.candidateId,
    title: product.title || l("Recommended product", "추천 상품"),
    productUrl: product.locator?.productUrl,
    sellerDomain:
      product.locator?.sellerDomain || product.previewVariant?.sellerDomain,
    mediaUrl: preview?.mediaUrl || product.mediaUrl,
    merchant:
      catalogPreview?.sellerName || product.locator?.sellerDomain || l("Shopify merchant", "Shopify merchant"),
    previewPriceMinor: preview?.priceMinor ?? product.priceMinimumMinor ?? 0,
    currency: preview?.currency || product.currency || "USD",
    previewVariant: preview,
    axisAssessment: product.axisAssessment,
    configuredVariantId: configured?.variant.variantId,
	preserveInitialVariantSelection: Boolean(configured),
	intentPoint:
	  product.intentPoint ||
	  product.description ||
		  l("This Shopify product matches your requested conditions.", "이 Shopify 상품은 요청하신 조건에 맞습니다."),
	features:
	  (product.features ?? []).length > 0
		? product.features ?? []
		: product.categories.slice(0, 3),
	specifications:
	  (product.specifications ?? []).length > 0
		? product.specifications ?? []
		: preview?.title
		  ? [`Preview · ${preview.title}`]
		  : [],
    searchPlatformMessages,
    targetTitle: target.title,
    curationPath: `/curations/${curationID}`,
  };
}

function cartItemCandidateView(item: LiveCartItem, l: Localize): CatalogCandidateView {
  return {
    curationId: currentCurationID(),
    targetId: item.targetId,
    candidateId: item.candidateId,
    title: item.productTitle,
    productUrl: item.productUrl,
    sellerDomain: item.sellerDomain,
    mediaUrl: item.productImageUrl,
    merchant: item.merchantName || l("Shopify merchant", "Shopify merchant"),
    previewPriceMinor: item.previewPriceMinor,
    currency: item.previewCurrency,
    previewVariant: {
      variantId: item.variantId,
      title: item.variantTitle,
      priceMinor: item.previewPriceMinor,
      currency: item.previewCurrency,
      available: false,
      selectedOptions: item.selectedOptions.map((option) => {
        const [name, ...value] = option.split(":");
        return { name: name.trim() || "Option", value: value.join(":").trim() || option };
      }),
      mediaUrl: item.productImageUrl,
      productUrl: item.productUrl,
    },
    preserveInitialVariantSelection: true,
    intentPoint: item.intentPoint || l("This product is in your cart.", "장바구니에 담은 상품입니다."),
    features: [],
    specifications: [],
    searchPlatformMessages: [],
    targetTitle: l("Cart", "장바구니"),
    curationPath: window.location.pathname,
  };
}

function currentCurationID(): string {
  return window.location.pathname.match(/^\/curations\/([^/]+)/)?.[1] ?? "";
}


function uniqueCandidateCount(
  products: LiveCatalogProduct[],
  hiddenProducts: LiveCatalogProduct[],
) {
  return new Set([
	...products.map(({ candidateId }) => candidateId),
	...hiddenProducts.map(({ candidateId }) => candidateId),
  ]).size;
}

function canonicalLiveSearchText(value: string) {
  return value.trim().replace(/\s+/g, " ");
}

function isAbortError(caught: unknown) {
  return caught instanceof DOMException
    ? caught.name === "AbortError"
    : Boolean(
        caught &&
          typeof caught === "object" &&
          "name" in caught &&
          caught.name === "AbortError",
      );
}

function messageOf(caught: unknown, l: Localize) {
 const conversationMessage=conversationError(caught,l);if(conversationMessage)return conversationMessage;
  return caught instanceof Error
    ? l("We couldn't complete the request. Try again.", "요청을 완료하지 못했습니다. 다시 시도해 주세요.")
    : l("We couldn't process the request.", "요청을 처리하지 못했습니다.");
}
