import { useEffect, useMemo, useRef, useState } from "react";
import {
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { ActionButton } from "../components/ActionButton";
import {
  AgentActivitySheet,
  type AgentActivityPresentation,
} from "../components/AgentActivitySheet";
import { AgentAvatar } from "../components/AgentAvatar";
import {
  BackgroundResearchPanel,
  type BackgroundResearchAction,
  type BackgroundResearchFindingPresentation,
  type BackgroundResearchMonitorPresentation,
} from "../components/BackgroundResearchPanel";
import { BlockingQuestionCard } from "../components/BlockingQuestionCard";
import { BrowserDestinationLauncher } from "../components/BrowserDestinationLauncher";
import {
  orderedBrowserDestinations,
  type BrowserDestinationTarget,
} from "../components/browserDestinations";
import {
  type BrowserControlMode,
  type BrowserRunPresentation,
} from "../components/BrowserRunPanel";
import { BrowserRunSheet } from "../components/BrowserRunSheet";
import { BrowserRuntimeViewport } from "../components/BrowserRuntimeViewport";
import type { BrowserRuntimeViewportHandle } from "../components/BrowserRuntimeViewport.types";
import type {
  BrowserRuntimePhase,
  BrowserRuntimeState,
} from "../components/browserRuntimeSession";
import {
  type BrowserSessionState,
} from "../components/BrowserSessionSurface";
import {
  BudgetSheet,
  type BudgetPreviewView,
} from "../components/BudgetSheet";
import {
  CandidateCard,
  type CandidateCardView,
  formatCandidatePrice,
} from "../components/CandidateCard";
import {
  ClarificationSheet,
  type ClarificationDraft,
  type ClarificationQuestionView,
} from "../components/ClarificationSheet";
import { IntentComposer } from "../components/IntentComposer";
import {
  ExternalLinkApprovalSheet,
  type ExternalLinkApprovalPresentation,
} from "../components/ExternalLinkApprovalSheet";
import { HumanHandoffSheet } from "../components/HumanHandoffSheet";
import {
  ShoppingGoalCard,
  type ShoppingGoalPresentation,
} from "../components/ShoppingGoalCard";
import {
  ShoppingMemorySheet,
  type ShoppingMemoryValue,
} from "../components/ShoppingMemorySheet";
import {
  ShoppingProposalCard,
  type ShoppingProposalPresentation,
} from "../components/ShoppingProposalCard";
import {
  StatusNotice,
  type StatusNoticeTone,
} from "../components/StatusNotice";
import {
  candidateSurfaceKind,
  projectBlockingQuestionSurface,
} from "../components/surfaceRegistry";
import type {
  AgentMessageResponse,
  BrowserRunView,
  BudgetChangePreview,
  CandidateView,
  CandidateProvenance,
  ConditionChipView,
  Locale,
  QuestionAnswerInput,
  QuestionView,
  ShoppingPreferencesPatch,
  WorkspaceView,
} from "../domain";
import { formatMoney } from "../domain";
import { useLocale } from "../i18n/LocaleProvider";
import type { MessageKey } from "../i18n/messages";
import { colors, radius, size, spacing, type } from "../theme/tokens";

type WorkspaceScreenProps = {
  workspace: WorkspaceView;
  busy: boolean;
  error: string | null;
  onAnswerQuestion: (answer: QuestionAnswerInput) => Promise<WorkspaceView | null>;
  onPreviewBudget: (amount: number) => Promise<BudgetChangePreview | null>;
  onApplyBudget: (preview: BudgetChangePreview) => Promise<WorkspaceView | null>;
  onClearError: () => void;
  onSubmitFollowUp: (text: string) => Promise<WorkspaceView | null>;
  onRefresh: () => Promise<WorkspaceView | null>;
  onRespondToAgentMessage?: (
    messageId: string,
    messageVersion: number,
    response: AgentMessageResponse,
  ) => Promise<WorkspaceView | null>;
  onCancelResearchSubscription?: (subscriptionId: string) => Promise<WorkspaceView | null>;
  onHideResearchFinding?: (findingId: string) => Promise<WorkspaceView | null>;
  onImportResearchFinding?: (findingId: string) => Promise<WorkspaceView | null>;
  onUpdateShoppingPreferences?: (patch: ShoppingPreferencesPatch) => Promise<WorkspaceView | null>;
  onStartBrowserRun?: (candidateId: string) => Promise<BrowserRunView | null>;
};

type ExternalNavigationTarget = {
  readonly url: string;
  readonly presentation: ExternalLinkApprovalPresentation;
  readonly runtimeMode?: "fixture" | "server" | "direct";
  readonly imageUrl?: string;
  readonly candidateId?: string;
  readonly durableRunId?: string;
};

const emptyDraft = (): ClarificationDraft => ({ customText: "", noPreference: false });

const draftFromQuestion = (question: QuestionView): ClarificationDraft => ({
  selectedOptionId: question.selectedOptionIds[0],
  customText: question.customText ?? "",
  noPreference: question.optional
    && question.state === "answered"
    && question.selectedOptionIds.length === 0
    && !question.customText,
});

export function WorkspaceScreen({
  workspace,
  busy,
  error,
  onAnswerQuestion,
  onPreviewBudget,
  onApplyBudget,
  onClearError,
  onSubmitFollowUp,
  onRefresh,
  onRespondToAgentMessage,
  onCancelResearchSubscription,
  onHideResearchFinding,
  onImportResearchFinding,
  onUpdateShoppingPreferences,
  onStartBrowserRun,
}: WorkspaceScreenProps) {
  const { locale, setLocale, t } = useLocale();
  const insets = useSafeAreaInsets();
  const scrollRef = useRef<ScrollView>(null);
  const browserViewportRef = useRef<BrowserRuntimeViewportHandle>(null);
  const scrollY = useRef(0);
  const previewGeneration = useRef(0);
  const [questionVisible, setQuestionVisible] = useState(false);
  const [questionId, setQuestionId] = useState(
    workspace.activeQuestionId ?? workspace.questions[0]?.id ?? "",
  );
  const [drafts, setDrafts] = useState<Record<string, ClarificationDraft>>({});
  const [inlineAnswering, setInlineAnswering] = useState<string | null>(null);
  const [budgetVisible, setBudgetVisible] = useState(false);
  const [draftBudget, setDraftBudget] = useState(workspace.plan.totals.budget.amount);
  const [budgetPreview, setBudgetPreview] = useState<BudgetChangePreview | null>(null);
  const [resultsExpanded, setResultsExpanded] = useState(true);
  const [followUp, setFollowUp] = useState("");
  const [activityVisible, setActivityVisible] = useState(false);
  const [memoryVisible, setMemoryVisible] = useState(false);
  const [memoryDraft, setMemoryDraft] = useState<ShoppingMemoryValue | null>(null);
  const [proposalResponse, setProposalResponse] = useState<{
    readonly id: string;
    readonly response: "accept" | "dismiss" | "acknowledge";
  } | null>(null);
  const [backgroundAction, setBackgroundAction] = useState<BackgroundResearchAction>();
  const [externalTarget, setExternalTarget] = useState<ExternalNavigationTarget | null>(null);
  const [externalError, setExternalError] = useState(false);
  const [browserTarget, setBrowserTarget] = useState<ExternalNavigationTarget | null>(null);
  const [browserControl, setBrowserControl] = useState<BrowserControlMode>("agent");
  const [browserSessionState, setBrowserSessionState] = useState<BrowserSessionState>("loading");
  const [browserRuntimeState, setBrowserRuntimeState] = useState<BrowserRuntimeState | null>(null);
  useEffect(() => {
    setDraftBudget(workspace.plan.totals.budget.amount);
    setBudgetPreview(null);
  }, [workspace.plan.totals.budget.amount]);
  useEffect(() => {
    if (!questionVisible && workspace.activeQuestionId) {
      setQuestionId(workspace.activeQuestionId);
    }
  }, [questionVisible, workspace.activeQuestionId]);
  useEffect(() => {
    if (!memoryVisible && workspace.shoppingPreferences?.availability === "available") {
      setMemoryDraft(workspace.shoppingPreferences.effective);
    }
  }, [memoryVisible, workspace.shoppingPreferences]);

  const question = workspace.questions.find((item) => item.id === questionId);
  const questionDraft = question
    ? drafts[question.id] ?? draftFromQuestion(question)
    : emptyDraft();
  const committedQuestionDraft = question ? draftFromQuestion(question) : emptyDraft();
  const linkedCondition = question
    ? workspace.conditions.find((condition) => condition.refinementQuestionId === question.id)
    : undefined;
  const hasQuestionDraftChanges = question
    ? questionDraft.selectedOptionId !== committedQuestionDraft.selectedOptionId
      || questionDraft.customText !== committedQuestionDraft.customText
      || questionDraft.noPreference !== committedQuestionDraft.noPreference
    : false;
  const hasUnappliedQuestionChanges = hasQuestionDraftChanges
    || Boolean(linkedCondition && !linkedCondition.confirmed);
  const questionView = question ? localizeQuestion(question, locale) : undefined;
  const assumptions = workspace.conditions.filter((condition) => {
    if (condition.confirmed || !condition.refinementQuestionId) return false;
    if (condition.refinementQuestionId === workspace.activeQuestionId) return false;
    const refinement = workspace.questions.find((item) => item.id === condition.refinementQuestionId);
    return refinement?.answerOrigin === "assumed";
  });
  const blockingQuestion = projectBlockingQuestionSurface(workspace, locale);

  const activeCandidates = useMemo(() => {
    const activeIds = new Set(workspace.activeCandidateIds);
    const selectedIds = new Set(workspace.selectedCandidateIds);
    const planItems = new Map(workspace.plan.items.map((item) => [item.id, item] as const));
    const serviceItemIds = new Set(
      workspace.plan.items.filter((item) => item.kind === "service").map((item) => item.id),
    );
    return workspace.candidates
      .filter((candidate) => activeIds.has(candidate.id))
      .map<CandidateCardView>((candidate) => {
        const serviceItem = candidate.itemIds
          .map((itemId) => planItems.get(itemId))
          .find((item) => item?.kind === "service" && item.state === "selected");
        const surfaceKind = candidateSurfaceKind(candidate, serviceItemIds);
        const timing = serviceItem?.slot
          ? formatServiceTiming(serviceItem.slot.start, locale)
          : serviceItem
            ? t("workspace.pickupPending")
            : workspace.mode === "fixture"
              ? t("workspace.delivery")
              : undefined;
        const quantity = candidate.itemIds.reduce(
          (total, itemId) => total + (planItems.get(itemId)?.quantity ?? 0),
          0,
        );
        const provenance = candidate.provenance;
        return {
          id: candidate.id,
          kind: surfaceKind === "candidate.service" ? "service" : "product",
          title: candidate.title[locale],
          source: provenance.source || workspace.sourceLabel[locale],
          option: candidate.description[locale],
          price: candidate.price,
          ...(quantity > 0 ? { quantity } : {}),
          reason: candidate.fitReasons.map((reason) => reason[locale]).join(" · "),
          ...(timing ? { timing } : {}),
          selected: selectedIds.has(candidate.id),
          visualLabel: candidate.badge[locale].slice(0, 2),
          visualTone: candidate.tone === "sage"
            ? "moss"
            : candidate.tone === "sand"
              ? "maple"
              : candidate.tone === "lavender"
                ? "iris"
                : "water",
          ...(candidate.imageUrl ? { imageUrl: candidate.imageUrl } : {}),
          ...(candidate.imageAlt ? { imageAlt: candidate.imageAlt[locale] } : {}),
          ...(formatProvenance(candidate.provenance) ? {
            provenance: formatProvenance(candidate.provenance),
          } : {}),
          ...(candidate.provenance.observedAt ? {
            observedAt: formatObservationTime(candidate.provenance.observedAt, locale),
          } : {}),
          ...(candidate.provenance.availability ? {
            availability: candidate.provenance.availability,
          } : {}),
        };
      });
  }, [locale, t, workspace]);

  const browserDestinationQuery = workspace.goal?.title[locale] ?? workspace.title[locale];
  const browserDestinations = useMemo(() => {
    try {
      return orderedBrowserDestinations(browserDestinationQuery, locale);
    } catch {
      return [];
    }
  }, [browserDestinationQuery, locale]);

  const goalPresentation = useMemo<ShoppingGoalPresentation | null>(() => (
    workspace.goal ? shoppingGoalPresentation(workspace, locale, t) : null
  ), [locale, t, workspace]);
  const activityItems = useMemo<AgentActivityPresentation[]>(() => (
    (workspace.activity ?? []).map((item) => ({
      id: item.id,
      title: item.label[locale],
      ...(item.body ? { detail: item.body } : {}),
      state: item.status === "complete"
        ? "completed"
        : item.status === "running"
          ? "running"
          : item.status === "failed"
            ? "failed"
            : item.status === "cancelled"
              ? "cancelled"
              : item.status === "review_required"
                ? "review-required"
                : "waiting",
      ...(item.occurredAt ? { timeLabel: formatObservationTime(item.occurredAt, locale) } : {}),
    }))
  ), [locale, workspace.activity]);
  const proposals = useMemo(() => (
    (workspace.agentMessages ?? []).map((message): {
      messageId: string;
      messageVersion: number;
      presentation: ShoppingProposalPresentation;
      available: boolean;
    } => {
      const available = !message.availableAt || Date.parse(message.availableAt) <= Date.now();
      return {
        messageId: message.id,
        messageVersion: message.version,
        available,
        presentation: {
          id: message.id,
          title: message.title[locale],
          body: message.body,
          ...(message.targetTitle ? { reason: message.targetTitle } : {}),
          status: message.status.toLocaleLowerCase("en-US") as ShoppingProposalPresentation["status"],
          responseMode: message.status !== "PENDING"
            ? "none"
            : message.kind === "PROPOSAL"
              ? "decision"
              : "acknowledge",
          ...(!available && message.availableAt
            ? { availableAtLabel: formatObservationTime(message.availableAt, locale) }
            : {}),
        },
      };
    })
  ), [locale, workspace.agentMessages]);
  const backgroundMonitors = useMemo<BackgroundResearchMonitorPresentation[]>(() => (
    (workspace.backgroundResearch?.subscriptions ?? []).map((subscription) => ({
      id: subscription.id,
      title: subscription.targetTitle,
      criteria: [
        subscription.keywords.join(" · "),
        subscription.maximum
          ? t("workspace.maximumPrice", { amount: formatMoney(subscription.maximum, locale) })
          : "",
      ].filter(Boolean).join(" · "),
      status: subscription.status.toLocaleLowerCase("en-US").replaceAll("_", "-") as BackgroundResearchMonitorPresentation["status"],
      expiresLabel: formatDate(subscription.expiresAt, locale),
    }))
  ), [locale, t, workspace.backgroundResearch]);
  const backgroundFindings = useMemo<BackgroundResearchFindingPresentation[]>(() => (
    (workspace.backgroundResearch?.findings ?? [])
      .filter((finding) => finding.status !== "HIDDEN")
      .map((finding) => ({
        id: finding.id,
        monitorId: finding.subscriptionId,
        title: finding.title,
        source: finding.provider,
        priceLabel: formatCandidatePrice(finding.price, locale, t("candidate.priceUnknown")),
        reason: finding.reason,
        observedLabel: formatObservationTime(finding.observedAt, locale),
        status: finding.status.toLocaleLowerCase("en-US") as BackgroundResearchFindingPresentation["status"],
        openable: Boolean(safeExternalUrl(finding.productUrl)),
      }))
  ), [locale, t, workspace.backgroundResearch]);

  const updateQuestionDraft = (draft: ClarificationDraft) => {
    if (!question) return;
    if (error) onClearError();
    setDrafts((current) => ({ ...current, [question.id]: draft }));
  };

  const answerQuestion = async () => {
    if (!question) return;
    const draft = drafts[question.id] ?? draftFromQuestion(question);
    const answer: QuestionAnswerInput = {
      questionId: question.id,
      selectedOptionIds: draft.selectedOptionId ? [draft.selectedOptionId] : [],
      ...(draft.customText.trim() ? { customText: draft.customText.trim() } : {}),
      disposition: draft.noPreference ? "no_preference" : "apply",
    };
    const next = await onAnswerQuestion(answer);
    if (next) {
      setDrafts((current) => {
        const nextDrafts = { ...current };
        delete nextDrafts[question.id];
        return nextDrafts;
      });
      setQuestionVisible(false);
    }
  };

  const answerBlockingQuestion = async (optionId: string) => {
    if (!blockingQuestion) return;
    onClearError();
    setInlineAnswering(optionId);
    try {
      const next = await onAnswerQuestion({
        questionId: blockingQuestion.questionId,
        selectedOptionIds: [optionId],
        disposition: "apply",
      });
      if (next) {
        setDrafts((current) => {
          const nextDrafts = { ...current };
          delete nextDrafts[blockingQuestion.questionId];
          return nextDrafts;
        });
      }
    } finally {
      setInlineAnswering(null);
    }
  };

  const openRefinement = (refinementQuestionId: string) => {
    const refinementQuestion = workspace.questions.find(
      (item) => item.id === refinementQuestionId,
    );
    if (!refinementQuestion) return;
    onClearError();
    setDrafts((current) => ({
      ...current,
      [refinementQuestionId]: draftFromQuestion(refinementQuestion),
    }));
    setQuestionId(refinementQuestionId);
    setQuestionVisible(true);
  };

  const closeQuestion = () => {
    onClearError();
    if (question) {
      setDrafts((current) => ({ ...current, [question.id]: draftFromQuestion(question) }));
    }
    setQuestionVisible(false);
  };

  const openBudget = () => {
    onClearError();
    setDraftBudget(workspace.plan.totals.budget.amount);
    setBudgetPreview(null);
    setBudgetVisible(true);
  };

  const changeDraftBudget = async (amount: number) => {
    if (error) onClearError();
    setDraftBudget(amount);
    setBudgetPreview(null);
    const generation = ++previewGeneration.current;
    const supportedFixtureBudget = workspace.budgetOptions.some((option) => option.amount === amount);
    if (
      amount <= 0
      || amount === workspace.plan.totals.budget.amount
      || (workspace.mode === "fixture" && !supportedFixtureBudget)
    ) return;
    const preview = await onPreviewBudget(amount);
    if (generation === previewGeneration.current) setBudgetPreview(preview);
  };

  const budgetPreviewView: BudgetPreviewView | undefined = budgetPreview ? {
    knownTotal: budgetPreview.projectedPlan.totals.knownTotal,
    remaining: budgetPreview.projectedPlan.totals.remainingBudget,
    overage: budgetPreview.projectedPlan.totals.overBudget,
    totalState: budgetPreview.projectedPlan.totals.totalState,
    comparisonState: budgetPreview.projectedPlan.totals.budgetComparisonState,
    summary: budgetPreview.changes.map((change) => change.after[locale]).join(" · "),
  } : undefined;
  const supportedFixtureBudget = workspace.budgetOptions.some((option) => option.amount === draftBudget);
  const budgetPreviewMessage = draftBudget <= 0
    ? t("budget.invalidAmount")
    : workspace.mode === "fixture"
      && draftBudget !== workspace.plan.totals.budget.amount
      && !supportedFixtureBudget
      ? t("budget.previewUnavailable", {
        amount: formatMoney({ currency: workspace.plan.totals.budget.currency, amount: draftBudget }, locale),
      })
      : undefined;

  const applyBudget = async () => {
    if (!budgetPreview) return;
    const anchor = scrollY.current;
    const next = await onApplyBudget(budgetPreview);
    if (next) {
      setBudgetVisible(false);
      setResultsExpanded(true);
      setTimeout(() => scrollRef.current?.scrollTo({ animated: false, y: anchor }), 0);
    }
  };

  const submitFollowUp = async () => {
    const value = followUp.trim();
    if (!value) return;
    const anchor = scrollY.current;
    const next = await onSubmitFollowUp(value);
    if (next) {
      setFollowUp("");
      setResultsExpanded(true);
      setTimeout(() => scrollRef.current?.scrollTo({ animated: false, y: anchor }), 0);
    }
  };

  const respondToProposal = async (
    messageId: string,
    messageVersion: number,
    response: AgentMessageResponse,
  ) => {
    if (!onRespondToAgentMessage) return;
    const responseLabel = response === "ACCEPT"
      ? "accept"
      : response === "DISMISS"
        ? "dismiss"
        : "acknowledge";
    setProposalResponse({ id: messageId, response: responseLabel });
    try {
      await onRespondToAgentMessage(messageId, messageVersion, response);
    } finally {
      setProposalResponse(null);
    }
  };

  const performBackgroundAction = async (
    action: BackgroundResearchAction,
    command: (() => Promise<WorkspaceView | null>) | undefined,
  ) => {
    if (!command) return;
    setBackgroundAction(action);
    try {
      await command();
    } finally {
      setBackgroundAction(undefined);
    }
  };

  const showBrowserCandidate = async (candidate: CandidateView) => {
    let url: string | null;
    let durableRunId: string | undefined;
    let destinationLabel: string;
    if (workspace.mode === "server") {
      if (!onStartBrowserRun) return;
      const run = await onStartBrowserRun(candidate.id);
      if (!run) return;
      url = safeExternalUrl(run.productUrl);
      if (!url) return;
      durableRunId = run.id;
      destinationLabel = run.merchantHost;
    } else {
      url = safeExternalUrl(candidate.provenance.productUrl);
      if (!url) return;
      destinationLabel = new URL(url).hostname;
    }
    setBrowserControl(workspace.mode === "server" ? "paused" : "agent");
    setBrowserSessionState("loading");
    setBrowserRuntimeState(null);
    setBrowserTarget({
      url,
      ...(durableRunId ? { durableRunId } : {}),
      presentation: {
        productTitle: candidate.title[locale],
        sellerName: candidate.provenance.source || destinationLabel,
        destinationLabel,
        priceLabel: formatCandidatePrice(candidate.price, locale, t("candidate.priceUnknown")),
      },
      ...(candidate.imageUrl ? { imageUrl: candidate.imageUrl } : {}),
    });
  };

  const showExternalFinding = (findingId: string) => {
    const finding = workspace.backgroundResearch?.findings.find((item) => item.id === findingId);
    if (!finding) return;
    const url = safeExternalUrl(finding.productUrl);
    if (!url) return;
    const parsed = new URL(url);
    setExternalError(false);
    setExternalTarget({
      url,
      ...(finding.candidateId ? { candidateId: finding.candidateId } : {}),
      presentation: {
        productTitle: finding.title,
        sellerName: finding.provider || parsed.hostname,
        destinationLabel: parsed.hostname,
        priceLabel: formatCandidatePrice(finding.price, locale, t("candidate.priceUnknown")),
      },
    });
  };

  const showBrowserDestination = (destination: BrowserDestinationTarget) => {
    const url = safeExternalUrl(destination.url);
    if (!url) return;
    setExternalError(false);
    setExternalTarget({
      url,
      runtimeMode: "direct",
      presentation: {
        productTitle: browserDestinationQuery,
        sellerName: destination.label,
        destinationLabel: destination.host,
        destinationKind: destination.kind,
      },
    });
  };

  const allowExternalNavigation = async () => {
    if (!externalTarget) return;
    let target = externalTarget;
    if (workspace.mode === "server" && externalTarget.candidateId) {
      if (!onStartBrowserRun) return;
      const run = await onStartBrowserRun(externalTarget.candidateId);
      const url = safeExternalUrl(run?.productUrl);
      if (!run || !url) {
        setExternalError(true);
        return;
      }
      target = {
        ...externalTarget,
        url,
        durableRunId: run.id,
        presentation: {
          ...externalTarget.presentation,
          destinationLabel: run.merchantHost,
        },
      };
    }
    setBrowserControl(target.runtimeMode === "direct"
      ? "user"
      : workspace.mode === "fixture"
        ? "agent"
        : "paused");
    setBrowserSessionState("loading");
    setBrowserRuntimeState(null);
    setBrowserTarget(target);
    setExternalTarget(null);
    setExternalError(false);
  };

  const browserRuntimeMode = browserTarget?.runtimeMode === "direct"
    ? "direct" as const
    : workspace.mode === "server" && browserTarget?.durableRunId
      ? "server" as const
      : "fixture" as const;
  const browserReviewOnly = (
    workspace.mode === "server" && browserRuntimeMode === "fixture"
  ) || (
    browserRuntimeMode !== "fixture" && Platform.OS !== "ios"
  );
  const browserUsesNativeRuntime = browserRuntimeMode !== "fixture";
  const browserPhase = browserUsesNativeRuntime
    ? browserRuntimeState?.phase ?? "loading"
    : browserSessionState;
  const runtimeControl = browserRuntimeMode === "direct"
    ? "user" as const
    : browserRuntimeMode === "server"
      ? browserRuntimeState?.controlMode ?? "paused"
      : browserControl;
  const browserRun: BrowserRunPresentation | null = browserTarget ? {
    id: browserTarget.durableRunId ?? `workspace-browser-review-${workspace.id}`,
    goal: browserTarget.presentation.productTitle,
    merchantName: browserTarget.presentation.sellerName,
    origin: browserUsesNativeRuntime
      ? browserRuntimeState?.origin ?? browserTarget.presentation.destinationLabel
      : browserTarget.presentation.destinationLabel,
    currentAction: browserRuntimeMode === "direct"
      ? t("browser.directAction", { value: browserTarget.presentation.sellerName })
      : browserRuntimeMode === "server"
      ? t(browserRuntimeActionKey(browserPhase))
      : browserControl === "paused"
      ? browserSessionState === "sign_in_required"
        ? t("browser.loginRequiredAction")
        : browserSessionState === "checkout_ready"
          ? t("browser.checkoutReadyAction")
          : t("browser.stoppedStatus")
      : browserSessionState === "signed_in"
        ? t("browser.signedInAction")
        : browserSessionState === "payment_handoff"
          ? t("browser.paymentHandoffAction")
          : browserControl === "user"
            ? t("browser.waitingForUser")
            : t("browser.preparingCandidate", { value: browserTarget.presentation.productTitle }),
    stepLabel: browserRuntimeMode === "direct"
      ? t("browser.directStep")
      : browserRuntimeMode === "server"
      ? t(browserRuntimeStepKey(browserPhase))
      : t(browserSessionStepKeys[browserSessionState]),
    controlMode: runtimeControl,
    state: browserPhase === "verifying"
      ? "verifying"
      : runtimeControl === "user"
        ? "needs-user"
        : "preparing",
    nativeOriginVerified: browserUsesNativeRuntime
      ? browserRuntimeState?.nativeOriginVerified ?? false
      : false,
    ...(browserRuntimeMode === "direct"
      ? { dataScope: t("browser.directUserBoundary") }
      : workspace.mode === "server"
        ? { dataScope: t("browser.deviceChannelUnavailable") }
        : {}),
  } : null;

  const openMemory = () => {
    const preferences = workspace.shoppingPreferences;
    if (preferences?.availability !== "available") return;
    onClearError();
    setMemoryDraft(preferences.effective);
    setMemoryVisible(true);
  };

  const applyMemory = async () => {
    if (!memoryDraft || !onUpdateShoppingPreferences) return;
    const next = await onUpdateShoppingPreferences(memoryDraft);
    if (next) {
      setLocale(memoryDraft.uiLocale);
      setMemoryVisible(false);
    }
  };

  const selectedItemCount = workspace.plan.items.filter((item) => item.state === "selected").length;
  const totals = workspace.plan.totals;
  const showPlanSummary = selectedItemCount > 0
    || workspace.selectedCandidateIds.length > 0
    || totals.knownTotal.amount > 0
    || totals.budgetState === "LIMITED"
    || (totals.totalState === "PARTIAL" && totals.hasUnknownFees);
  const showBackgroundResearch = backgroundFindings.length > 0
    || backgroundMonitors.some((monitor) => monitor.status === "active");
  const processingStatus = workspace.processing?.status;
  const agentStatusKey = processingStatus === "FAILED"
    ? "workspace.agentFailedStatus"
    : processingStatus === "CANCELLED"
      ? "workspace.agentCancelledStatus"
      : processingStatus === "RESULT_CONFIRMATION_REQUIRED"
        ? "workspace.agentReviewStatus"
      : processingStatus === "WAITING_SELECTION"
      ? "workspace.agentWaitingStatus"
      : workspace.processing?.shouldPoll
        ? "workspace.agentWorkingStatus"
        : "workspace.agentReadyStatus";
  const assistantCopy = blockingQuestion
    ? t("workspace.assistantQuestion")
    : processingStatus === "FAILED"
      ? t("workspace.assistantFailed")
      : processingStatus === "CANCELLED"
        ? t("workspace.assistantCancelled")
        : processingStatus === "RESULT_CONFIRMATION_REQUIRED"
          ? t("workspace.assistantReview")
        : processingStatus === "WAITING_SELECTION"
          ? t("workspace.assistantWaiting")
          : workspace.processing?.shouldPoll
            ? workspace.processing.label
            : t("workspace.assistantComplete");
  const agentStatusTone = processingStatus === "FAILED"
    ? styles.agentStatusDotFailed
    : processingStatus === "CANCELLED"
      || processingStatus === "WAITING_SELECTION"
      || processingStatus === "RESULT_CONFIRMATION_REQUIRED"
      ? styles.agentStatusDotWarning
      : workspace.processing?.shouldPoll
        ? styles.agentStatusDotWorking
        : styles.agentStatusDotReady;
  const providerNoticeTone = workspaceNoticeTone(workspace);

  return (
    <View style={styles.screen}>
      <View style={styles.agentHeader}>
        <Pressable
          accessibilityHint={t("goal.openActivity")}
          accessibilityRole="button"
          onPress={() => setActivityVisible(true)}
          style={({ pressed }) => [styles.agentIdentity, pressed && styles.pressed]}
          testID="workspace.open-activity"
        >
          <AgentAvatar active size={42} />
          <View style={styles.agentHeaderCopy}>
            <Text style={styles.agentName}>{t("agent.name")}</Text>
            <View style={styles.agentStatusRow}>
              <View testID="workspace.agent-status-dot" style={[styles.agentStatusDot, agentStatusTone]} />
              <Text style={styles.agentStatus}>{t(agentStatusKey)}</Text>
            </View>
          </View>
        </Pressable>
        {workspace.shoppingPreferences?.availability === "available" && onUpdateShoppingPreferences ? (
          <ActionButton
            compact
            emphasis="quiet"
            label={t("memory.title")}
            onPress={openMemory}
          />
        ) : null}
        <Text style={styles.revision}>{t("workspace.revision", { revision: Math.max(0, workspace.revision - 1) })}</Text>
      </View>
      <ScrollView
        contentContainerStyle={styles.page}
        keyboardShouldPersistTaps="handled"
        onScroll={(event) => { scrollY.current = event.nativeEvent.contentOffset.y; }}
        ref={scrollRef}
        scrollEventThrottle={16}
      >
        <View style={styles.content}>
          <View style={styles.userMessage}>
            <Text style={styles.messageLabel}>{t("workspace.requestLabel")}</Text>
            <Text style={styles.userMessageText}>{workspace.intent}</Text>
          </View>

          <View style={styles.assistantMessage}>
            <AgentAvatar active size={34} />
            <View style={styles.assistantBubble}>
              <Text style={styles.assistantText}>{assistantCopy}</Text>
            </View>
          </View>
          {processingStatus === "WAITING_SELECTION" && !blockingQuestion ? (
            <View style={styles.statusRecoveryAction}>
              <ActionButton
                busy={busy}
                compact
                emphasis="secondary"
                label={t("workspace.refreshStatus")}
                onPress={() => { void onRefresh(); }}
              />
            </View>
          ) : null}

          {goalPresentation ? (
            <View style={styles.goalCard}>
              <ShoppingGoalCard
                goal={goalPresentation}
                onOpenActivity={() => setActivityVisible(true)}
              />
            </View>
          ) : null}

          {workspace.lastChange.length > 0 ? (
            <View style={styles.receiptMessage}>
              <AgentAvatar active size={34} />
              <View accessibilityLiveRegion="polite" style={styles.changeReceipt}>
                <Text style={styles.changeTitle}>{t("workspace.changeReceipt")}</Text>
                {workspace.lastChange.map((change) => (
                  <Text key={change[locale]} style={styles.changeLine}>✓ {change[locale]}</Text>
                ))}
              </View>
            </View>
          ) : null}

          {proposals.length > 0 ? (
            <View style={styles.proposals}>
              {proposals.map(({ messageId, messageVersion, presentation, available }) => (
                <ShoppingProposalCard
                  acceptDisabled={!available}
                  busyResponse={proposalResponse?.id === messageId ? proposalResponse.response : undefined}
                  key={messageId}
                  onAccept={onRespondToAgentMessage
                    ? () => { void respondToProposal(messageId, messageVersion, "ACCEPT"); }
                    : undefined}
                  onAcknowledge={onRespondToAgentMessage
                    ? () => { void respondToProposal(messageId, messageVersion, "ACKNOWLEDGE"); }
                    : undefined}
                  onDismiss={onRespondToAgentMessage
                    ? () => { void respondToProposal(messageId, messageVersion, "DISMISS"); }
                    : undefined}
                  proposal={presentation}
                />
              ))}
            </View>
          ) : null}

          <View style={styles.artifact}>
            <View style={styles.artifactBody}>
              {blockingQuestion ? (
                <BlockingQuestionCard
                  busyOptionId={inlineAnswering}
                  disabled={busy}
                  error={error && !questionVisible && !budgetVisible ? t("question.error") : undefined}
                  onChoose={answerBlockingQuestion}
                  onOpenEditor={() => openRefinement(blockingQuestion.questionId)}
                  surface={blockingQuestion}
                />
              ) : null}

              {assumptions.length > 0 ? (
                <View style={styles.assumptionsSection}>
                  <Text accessibilityRole="header" style={styles.assumptionsTitle}>
                    {t("workspace.assumptionsTitle")}
                  </Text>
                  <Text style={styles.assumptionsDescription}>
                    {t("workspace.assumptionsDescription")}
                  </Text>
                  <View style={styles.assumptionList}>
                    {assumptions.map((condition) => (
                      <AssumptionCard
                        condition={condition}
                        key={condition.id}
                        locale={locale}
                        onPress={condition.refinementQuestionId
                          ? () => openRefinement(condition.refinementQuestionId!)
                          : undefined}
                        t={t}
                      />
                    ))}
                  </View>
                </View>
              ) : null}

              {showPlanSummary ? (
                <View style={styles.planSummary}>
                  <View style={styles.planHeader}>
                    <View>
                      <Text style={styles.sectionLabel}>{t("workspace.summary")}</Text>
                      <Text style={styles.planMeta}>{t("workspace.itemCount", { count: selectedItemCount })}</Text>
                    </View>
                    <ActionButton compact emphasis="secondary" label={t("workspace.adjustBudget")} onPress={openBudget} />
                  </View>
                  <View style={styles.totalRow}>
                    <View>
                      <Text style={styles.totalLabel}>{t(
                        totals.totalState === "PARTIAL" ? "workspace.partialTotal" : "workspace.total",
                      )}</Text>
                      <Text style={styles.total}>{formatMoney(totals.knownTotal, locale)}</Text>
                    </View>
                    <View style={styles.budgetColumn}>
                      <Text style={styles.totalLabel}>{t("workspace.budget")}</Text>
                      <Text style={styles.budgetAmount}>
                        {workspace.plan.totals.budgetState === "UNLIMITED"
                          ? t("budget.unlimited")
                          : formatMoney(workspace.plan.totals.budget, locale)}
                      </Text>
                    </View>
                  </View>
                  {totals.budgetState === "LIMITED" ? (
                    totals.budgetComparisonState !== "AVAILABLE" ? (
                      <View style={styles.comparisonWarning}>
                        <Text style={styles.comparisonWarningText}>
                          {t("workspace.budgetComparisonUnavailable")}
                        </Text>
                      </View>
                    ) : totals.overBudget.amount > 0 ? (
                      <View style={styles.overBudgetPill}>
                        <Text style={styles.overBudgetText}>{t("workspace.overBudget", {
                          amount: formatMoney(totals.overBudget, locale),
                        })}</Text>
                      </View>
                    ) : (
                      <View style={styles.remainingPill}>
                        <Text style={styles.remaining}>{t("workspace.remaining", {
                          amount: formatMoney(totals.remainingBudget, locale),
                        })}</Text>
                      </View>
                    )
                  ) : null}
                </View>
              ) : null}

              <View style={styles.resultsHeader}>
                <View style={styles.headingCopy}>
                  <Text accessibilityRole="header" style={styles.sectionTitle}>{t("workspace.results")}</Text>
                  {activeCandidates.length > 0 ? (
                    <Text style={styles.resultCount}>
                      {t("workspace.resultCount", { count: activeCandidates.length })}
                    </Text>
                  ) : null}
                </View>
                {activeCandidates.length > 0 ? (
                  <ActionButton
                    compact
                    emphasis="quiet"
                    label={resultsExpanded ? t("workspace.collapseResults") : t("workspace.expandResults")}
                    onPress={() => setResultsExpanded((value) => !value)}
                  />
                ) : null}
              </View>
              {workspace.mode === "server" ? (
                <StatusNotice
                  message={workspace.notice[locale]}
                  testID="workspace.provider-notice"
                  tone={providerNoticeTone}
                />
              ) : null}
              {resultsExpanded || activeCandidates.length === 0 ? (
                <View style={styles.candidates}>
                  {activeCandidates.map((candidate) => {
                    const sourceCandidate = workspace.candidates.find((item) => item.id === candidate.id);
                    const canOpenSeller = Boolean(sourceCandidate) && (
                      workspace.mode === "server"
                      || Boolean(safeExternalUrl(sourceCandidate?.provenance.productUrl))
                    );
                    return (
                      <View key={candidate.id} style={styles.candidateStack}>
                        <CandidateCard
                          candidate={candidate}
                          onOpenBrowser={sourceCandidate && canOpenSeller
                            ? () => { void showBrowserCandidate(sourceCandidate); }
                            : undefined}
                        />
                      </View>
                    );
                  })}
                  {activeCandidates.length === 0 && workspace.processing?.shouldPoll ? (
                    <Text accessibilityLiveRegion="polite" style={styles.resultsPending}>
                      {t("workspace.resultsPending")}
                    </Text>
                  ) : activeCandidates.length === 0 ? (
                    <Text accessibilityLiveRegion="polite" style={styles.resultsEmpty}>
                      {t(processingStatus === "FAILED"
                        ? "workspace.resultsFailed"
                        : "workspace.resultsEmpty")}
                    </Text>
                  ) : null}
                </View>
              ) : (
                <Pressable
                  accessibilityRole="button"
                  onPress={() => setResultsExpanded(true)}
                  style={styles.collapsedResults}
                >
                  <View style={styles.collapsedVisuals}>
                    {activeCandidates.slice(0, 3).map((candidate) => (
                      <View key={candidate.id} style={styles.collapsedDot}>
                        <Text style={styles.collapsedDotText}>{candidate.visualLabel.slice(0, 1)}</Text>
                      </View>
                    ))}
                  </View>
                  <Text style={styles.collapsedText}>{t("workspace.resultsCollapsed")}</Text>
                </Pressable>
              )}
            </View>
          </View>

          <View style={styles.destinationPanel}>
            <BrowserDestinationLauncher
              destinations={browserDestinations}
              onSelect={showBrowserDestination}
            />
          </View>

          {workspace.backgroundResearch && showBackgroundResearch ? (
            <View style={styles.backgroundPanel}>
              <BackgroundResearchPanel
                busyAction={backgroundAction}
                findings={backgroundFindings}
                monitors={backgroundMonitors}
                onAddFinding={onImportResearchFinding
                  ? (findingId) => { void performBackgroundAction(
                    { kind: "add-finding", id: findingId },
                    () => onImportResearchFinding(findingId),
                  ); }
                  : undefined}
                onCancelMonitor={onCancelResearchSubscription
                  ? (subscriptionId) => { void performBackgroundAction(
                    { kind: "cancel-monitor", id: subscriptionId },
                    () => onCancelResearchSubscription(subscriptionId),
                  ); }
                  : undefined}
                onHideFinding={onHideResearchFinding
                  ? (findingId) => { void performBackgroundAction(
                    { kind: "hide-finding", id: findingId },
                    () => onHideResearchFinding(findingId),
                  ); }
                  : undefined}
                onOpenFinding={showExternalFinding}
                unavailableMessage={workspace.backgroundResearch.availability === "disabled"
                  ? t("research.unavailable")
                  : undefined}
              />
            </View>
          ) : null}

        </View>
      </ScrollView>

      <View style={[styles.composerDock, { paddingBottom: Math.max(insets.bottom, spacing[3]) }]}>
          <View style={styles.composerDockInner}>
            <View style={styles.followUpHeader}>
              <Text style={styles.followUpTitle}>{t("workspace.nextIdeas")}</Text>
              <View style={styles.followUpChips}>
                {workspace.followUpSuggestions.map((suggestion) => (
                  <FollowUpChip
                    key={suggestion.id}
                    label={suggestion.text[locale]}
                    onPress={() => setFollowUp(suggestion.text[locale])}
                  />
                ))}
              </View>
            </View>
            {error ? <Text accessibilityRole="alert" style={styles.error}>{t("workspace.changeFailed")}</Text> : null}
            <IntentComposer
              busy={busy}
              compact
              inputTestID="workspace.composer"
              onChange={setFollowUp}
              onSubmit={submitFollowUp}
              placeholder={t("workspace.followUpPlaceholder")}
              submitLabel={t("workspace.sendFollowUp")}
              submitTestID="workspace.send"
              value={followUp}
            />
          </View>
      </View>

      <ClarificationSheet
        current={1}
        draft={questionDraft}
        hasUnappliedChanges={hasUnappliedQuestionChanges}
        error={error ? t("question.error") : undefined}
        onApply={answerQuestion}
        onClose={closeQuestion}
        onDraftChange={updateQuestionDraft}
        question={questionView}
        runtimeMode={workspace.mode}
        total={1}
        visible={questionVisible}
        working={busy}
      />
      <BudgetSheet
        budgetOptions={workspace.budgetOptions.map((option) => option.amount)}
        currency={workspace.plan.totals.budget.currency}
        currentBudget={workspace.plan.totals.budget.amount}
        currentBudgetUnlimited={workspace.plan.totals.budgetState === "UNLIMITED"}
        draftBudget={draftBudget}
        error={error ? t("budget.error") : undefined}
        onApply={applyBudget}
        onClose={() => {
          onClearError();
          setBudgetVisible(false);
        }}
        onDraftChange={changeDraftBudget}
        preview={budgetPreviewView}
        previewMessage={budgetPreviewMessage}
        runtimeMode={workspace.mode}
        visible={budgetVisible}
        working={busy}
      />
      <AgentActivitySheet
        items={activityItems}
        onClose={() => setActivityVisible(false)}
        status={workspace.processing?.label ?? t(agentStatusKey)}
        visible={activityVisible}
      />
      {workspace.shoppingPreferences?.availability === "available" && memoryDraft ? (
        <ShoppingMemorySheet
          currentValue={workspace.shoppingPreferences.effective}
          draftValue={memoryDraft}
          error={error && memoryVisible ? t("memory.error") : undefined}
          onApply={() => { void applyMemory(); }}
          onClose={() => {
            onClearError();
            setMemoryVisible(false);
            setMemoryDraft(workspace.shoppingPreferences?.effective ?? memoryDraft);
          }}
          onDraftChange={setMemoryDraft}
          visible={memoryVisible}
          working={busy}
        />
      ) : null}
      <ExternalLinkApprovalSheet
        allowLabel={externalTarget?.runtimeMode === "direct"
          ? undefined
          : workspace.mode === "fixture"
            ? t("run.start")
            : undefined}
        error={externalError ? t("externalLink.error") : undefined}
        onAllow={() => { void allowExternalNavigation(); }}
        onDeny={() => {
          setExternalError(false);
          setExternalTarget(null);
        }}
        target={externalTarget?.presentation ?? null}
        visible={externalTarget !== null}
        working={busy}
      />
      {browserRun ? (
        <BrowserRunSheet
          nativeViewport={browserUsesNativeRuntime && Platform.OS === "ios"}
          onClose={() => {
            setBrowserRuntimeState(null);
            setBrowserTarget(null);
          }}
          onOpenActivity={browserRuntimeMode === "direct" ? undefined : () => setActivityVisible(true)}
          onResume={browserRuntimeMode === "direct"
            ? undefined
            : browserRuntimeMode === "server"
            ? browserRuntimeState?.controlMode === "user"
              && (browserPhase === "sign_in_required" || browserPhase === "needs_user")
              ? () => {
                  if (Platform.OS === "ios") void browserViewportRef.current?.requestResumeVerification();
                  else if (Platform.OS === "web") {
                    setBrowserSessionState("signed_in");
                    setBrowserControl("paused");
                  }
                }
              : undefined
            : browserSessionState !== "payment_handoff"
              && browserSessionState !== "sign_in_required"
              ? () => setBrowserControl("agent")
              : undefined}
          onStop={browserRuntimeMode === "direct"
            ? undefined
            : browserRuntimeMode === "server"
            ? () => {
                if (Platform.OS === "ios") void browserViewportRef.current?.stop();
                else setBrowserControl("paused");
              }
            : () => setBrowserControl("paused")}
          onTakeOver={browserRuntimeMode === "direct"
            ? undefined
            : browserRuntimeMode === "server"
            ? () => {
                if (Platform.OS === "ios") void browserViewportRef.current?.takeOver();
                else if (Platform.OS === "web") setBrowserControl("user");
              }
            : () => setBrowserControl("user")}
          page={browserTarget ? (
            <BrowserRuntimeViewport
              candidateUrl={browserTarget.url}
              controlMode={browserControl}
              onFixturePageReady={() => setBrowserSessionState((current) => (
                current === "loading" ? "product" : current
              ))}
              onFixturePrepareCheckout={() => {
                setBrowserControl("paused");
                setBrowserSessionState("checkout_ready");
              }}
              onFixturePreparePurchase={() => {
                setBrowserControl("paused");
                setBrowserSessionState("sign_in_required");
              }}
              onFixtureSignInCompleted={() => {
                setBrowserSessionState("signed_in");
                setBrowserControl("agent");
              }}
              onRuntimeStateChange={setBrowserRuntimeState}
              ref={browserViewportRef}
              reviewMode={browserReviewOnly}
              runId={browserTarget.durableRunId ?? `workspace-browser-review-${workspace.id}`}
              runtimeMode={browserRuntimeMode}
              session={{
                imageUrl: browserTarget.imageUrl,
                merchantName: browserTarget.presentation.sellerName,
                priceLabel: browserTarget.presentation.priceLabel,
                productTitle: browserTarget.presentation.productTitle,
                state: browserSessionState,
              }}
            />
          ) : undefined}
          reviewMode={browserReviewOnly}
          run={browserRun}
          visible
        />
      ) : null}
      <HumanHandoffSheet
        handoff={browserTarget && browserRuntimeMode !== "direct" && browserPhase !== "checkout_ready" && (
          (browserRuntimeMode === "fixture" && browserSessionState === "sign_in_required")
          || (browserRuntimeMode === "server" && (browserPhase === "sign_in_required" || browserPhase === "needs_user"))
        )
          ? {
              reason: browserRuntimeMode === "server"
                ? browserRuntimeState?.handoffReason === "verification"
                  ? "verification"
                  : browserRuntimeState?.handoffReason === "login"
                    ? "login"
                    : "unsupported"
                : "login",
              merchantName: browserTarget.presentation.sellerName,
              ...(browserRuntimeMode === "fixture" ? { detail: t("handoff.loginBody") } : {}),
            }
          : null}
        onStop={() => setBrowserTarget(null)}
        onTakeOver={() => {
          if (browserRuntimeMode === "server" && Platform.OS === "ios") void browserViewportRef.current?.takeOver();
          else if (browserRuntimeMode === "server" && Platform.OS === "web") setBrowserControl("user");
          else setBrowserControl("user");
        }}
        visible={Boolean(
          browserTarget
          && browserRuntimeMode !== "direct"
          && browserPhase !== "checkout_ready"
          && (browserRuntimeMode === "fixture"
            ? browserSessionState === "sign_in_required" && browserControl !== "user"
            : (browserPhase === "sign_in_required" || browserPhase === "needs_user")
              && browserRuntimeState?.controlMode !== "user"),
        )}
      />
      <HumanHandoffSheet
        handoff={browserTarget && browserRuntimeMode !== "direct" && browserPhase === "checkout_ready"
          ? {
              reason: "checkout-approval",
              merchantName: browserTarget.presentation.sellerName,
              quoteSummary: [
                browserTarget.presentation.productTitle,
                browserTarget.presentation.priceLabel,
              ].filter(Boolean).join(" · "),
            }
          : null}
        onStop={() => setBrowserTarget(null)}
        onTakeOver={() => {
          if (browserRuntimeMode === "server" && Platform.OS === "ios") {
            void browserViewportRef.current?.approveCheckout();
          } else if (browserRuntimeMode === "server" && Platform.OS === "web") {
            setBrowserSessionState("payment_handoff");
            setBrowserControl("user");
          }
          else {
            setBrowserSessionState("payment_handoff");
            setBrowserControl("user");
          }
        }}
        visible={Boolean(browserTarget && browserRuntimeMode !== "direct" && browserPhase === "checkout_ready")}
      />
    </View>
  );
}

function localizeQuestion(question: QuestionView, locale: Locale): ClarificationQuestionView {
  return {
    id: question.id,
    title: question.title[locale],
    reason: question.reason[locale],
    optional: question.optional,
    allowCustom: question.allowCustom,
    options: question.options.map((option) => ({
      id: option.id,
      title: option.title[locale],
      description: option.description[locale],
    })),
  };
}

function formatServiceTiming(value: string, locale: Locale): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
    timeZone: "Asia/Seoul",
  }).format(parsed);
}

function formatObservationTime(value: string, locale: Locale): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "short",
    timeStyle: "short",
  }).format(parsed);
}

function formatProvenance(provenance: CandidateProvenance): string {
  return [...new Set([
    provenance.apiProvider,
    provenance.apiProduct,
    provenance.discoveryChannel,
  ].filter((value): value is string => Boolean(value?.trim())))]
    .join(" · ");
}

function workspaceNoticeTone(workspace: WorkspaceView): StatusNoticeTone {
  if (workspace.noticeTone) return workspace.noticeTone;
  if (workspace.sourceCoverage?.some((source) => source.status === "FAILED")) {
    return "danger";
  }
  if (workspace.sourceCoverage?.some((source) => (
    source.status === "PARTIAL"
    || source.status === "UNSUPPORTED"
    || source.status === "SKIPPED"
  ))) {
    return "warning";
  }
  return "positive";
}

type Translator = (key: MessageKey, values?: Record<string, string | number>) => string;

function shoppingGoalPresentation(
  workspace: WorkspaceView,
  locale: Locale,
  t: Translator,
): ShoppingGoalPresentation {
  const goal = workspace.goal!;
  const state: ShoppingGoalPresentation["state"] = goal.status === "working"
    ? "researching"
    : goal.status === "needs_input"
      ? "needs-input"
      : goal.status === "review_required"
        ? "review-required"
        : goal.status === "failed"
          ? "failed"
          : "ready";
  const exactProgress = goal.coverage === "COMPLETE"
    ? 1
    : goal.coverage === "NONE"
      ? 0
      : undefined;
  return {
    id: workspace.id,
    title: goal.title[locale],
    summary: t("goal.summary", {
      targetCount: goal.activeTargetCount,
      candidateCount: goal.candidateCount,
      selectedCount: goal.selectedCount,
    }),
    state,
    ...(exactProgress === undefined ? {} : { progress: exactProgress }),
    progressDetail: t(goal.coverage === "COMPLETE"
      ? "goal.coverageComplete"
      : goal.coverage === "PARTIAL"
        ? "goal.coveragePartial"
        : "goal.coverageNone"),
  };
}

function formatDate(value: string, locale: Locale): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(parsed);
}

function safeExternalUrl(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const parsed = new URL(value);
    if ((parsed.protocol !== "https:" && parsed.protocol !== "http:") || parsed.username || parsed.password) {
      return null;
    }
    return parsed.toString();
  } catch {
    return null;
  }
}

const browserSessionStepKeys = {
  loading: "browser.sessionStepLoading",
  product: "browser.sessionStepProduct",
  sign_in_required: "browser.sessionStepSignIn",
  signed_in: "browser.sessionStepSignedIn",
  checkout_ready: "browser.sessionStepCheckout",
  payment_handoff: "browser.sessionStepPayment",
} as const satisfies Record<BrowserSessionState, MessageKey>;

function browserRuntimeActionKey(phase: BrowserRuntimePhase): MessageKey {
  switch (phase) {
    case "loading": return "browser.loadingProduct";
    case "product": return "browser.nativeBrowserReady";
    case "sign_in_required": return "browser.loginRequiredAction";
    case "verifying": return "browser.resumeVerifying";
    case "signed_in": return "browser.nativeSessionVerified";
    case "session_unknown": return "browser.nativeSessionUnknown";
    case "checkout_ready": return "browser.checkoutReadyAction";
    case "payment_handoff": return "browser.paymentHandoffAction";
    case "needs_user": return "browser.waitingForUser";
    case "unavailable":
    case "error":
      return "browser.deviceChannelAction";
  }
}

function browserRuntimeStepKey(phase: BrowserRuntimePhase): MessageKey {
  switch (phase) {
    case "loading": return "browser.sessionStepLoading";
    case "product": return "browser.sessionStepProduct";
    case "sign_in_required": return "browser.sessionStepSignIn";
    case "verifying": return "browser.resumeVerifying";
    case "signed_in": return "browser.sessionStepSignedIn";
    case "session_unknown": return "browser.sessionStepProduct";
    case "checkout_ready": return "browser.sessionStepCheckout";
    case "payment_handoff": return "browser.sessionStepPayment";
    case "needs_user": return "browser.sessionStepSignIn";
    case "unavailable":
    case "error":
      return "browser.fixtureStep";
  }
}

function AssumptionCard({
  condition,
  locale,
  onPress,
  t,
}: {
  condition: ConditionChipView;
  locale: Locale;
  onPress?: () => void;
  t: Translator;
}) {
  const content = (
    <>
      <View style={styles.assumptionCopy}>
        <View style={styles.assumptionLabelRow}>
          <Text style={styles.assumptionLabel}>{condition.label[locale]}</Text>
          <View style={styles.assumptionBadge}>
            <Text style={styles.assumptionBadgeText}>{t("workspace.assumptionBadge")}</Text>
          </View>
        </View>
        <Text style={styles.assumptionValue}>{condition.value[locale]}</Text>
        {condition.reason ? (
          <Text style={styles.assumptionReason}>{condition.reason[locale]}</Text>
        ) : null}
      </View>
      {onPress ? (
        <View style={styles.assumptionAction}>
          <Text style={styles.assumptionActionText}>{t("workspace.changeAssumption")}</Text>
          <Text accessibilityElementsHidden style={styles.assumptionArrow}>›</Text>
        </View>
      ) : null}
    </>
  );

  if (!onPress) {
    return <View style={styles.assumptionCard}>{content}</View>;
  }

  return (
    <Pressable
      accessibilityHint={t("workspace.changeAssumptionHint")}
      accessibilityLabel={`${condition.label[locale]}, ${condition.value[locale]}, ${t("workspace.assumptionBadge")}`}
      accessibilityRole="button"
      onPress={onPress}
      style={({ pressed }) => [styles.assumptionCard, pressed && styles.pressed]}
      testID={`assumption.${condition.id}`}
    >
      {content}
    </Pressable>
  );
}

function FollowUpChip({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <Pressable
      accessibilityRole="button"
      onPress={onPress}
      style={({ pressed }) => [styles.followUpChip, pressed && styles.pressed]}
    >
      <Text style={styles.followUpChipText}>{label}</Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  screen: { backgroundColor: colors.surface, flex: 1 },
  agentHeader: {
    alignItems: "center",
    backgroundColor: colors.surface,
    borderBottomColor: colors.border,
    borderBottomWidth: StyleSheet.hairlineWidth,
    flexDirection: "row",
    gap: spacing[3],
    minHeight: 66,
    paddingHorizontal: spacing[4],
  },
  agentIdentity: { alignItems: "center", flex: 1, flexDirection: "row", gap: spacing[3], minWidth: 0 },
  agentHeaderCopy: { flex: 1, minWidth: 0 },
  agentName: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  agentStatusRow: { alignItems: "center", flexDirection: "row", gap: spacing[1] },
  agentStatusDot: { borderRadius: radius.pill, height: 7, width: 7 },
  agentStatusDotReady: { backgroundColor: colors.positive },
  agentStatusDotWorking: { backgroundColor: colors.action },
  agentStatusDotWarning: { backgroundColor: colors.warning },
  agentStatusDotFailed: { backgroundColor: colors.danger },
  agentStatus: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  page: { alignItems: "center", backgroundColor: colors.canvas, padding: spacing[4], paddingBottom: spacing[8] },
  content: { maxWidth: size.contentMax, width: "100%" },
  headingCopy: { flex: 1 },
  revision: { color: colors.textMuted, fontSize: 11, lineHeight: 17 },
  userMessage: {
    alignSelf: "flex-end",
    backgroundColor: colors.surfaceSelected,
    borderBottomRightRadius: radius.control,
    borderRadius: radius.sheet,
    maxWidth: "88%",
    padding: spacing[4],
  },
  messageLabel: { color: colors.textMuted, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  userMessageText: { color: colors.text, fontSize: 17, lineHeight: 26, marginTop: spacing[1] },
  assistantMessage: { alignItems: "flex-start", flexDirection: "row", gap: spacing[2], marginTop: spacing[5] },
  assistantBubble: { backgroundColor: colors.surface, borderBottomLeftRadius: radius.control, borderRadius: radius.sheet, flex: 1, padding: spacing[4] },
  assistantText: { color: colors.text, fontSize: 17, lineHeight: 26 },
  statusRecoveryAction: { alignItems: "flex-start", marginLeft: 42, marginTop: spacing[2] },
  goalCard: { marginTop: spacing[4] },
  receiptMessage: { alignItems: "flex-start", flexDirection: "row", gap: spacing[2], marginTop: spacing[4] },
  proposals: { gap: spacing[3], marginTop: spacing[4] },
  assumptionsSection: {
    backgroundColor: colors.warningSoft,
    borderRadius: radius.overlay,
    gap: spacing[2],
    padding: spacing[4],
  },
  assumptionsTitle: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  assumptionsDescription: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  assumptionList: { gap: spacing[2], marginTop: spacing[1] },
  assumptionCard: {
    alignItems: "center",
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.overlay,
    borderWidth: StyleSheet.hairlineWidth,
    flexDirection: "row",
    gap: spacing[3],
    minHeight: 78,
    padding: spacing[3],
  },
  assumptionCopy: { flex: 1 },
  assumptionLabelRow: { alignItems: "center", flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  assumptionLabel: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  assumptionBadge: { backgroundColor: colors.warningSoft, borderRadius: radius.pill, paddingHorizontal: spacing[2], paddingVertical: 2 },
  assumptionBadgeText: { color: colors.warning, fontSize: 10, fontWeight: "600", lineHeight: 14 },
  assumptionValue: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine, marginTop: spacing[1] },
  assumptionReason: { color: colors.textMuted, fontSize: 12, lineHeight: 18, marginTop: spacing[1] },
  assumptionAction: { alignItems: "center", flexDirection: "row", gap: spacing[1] },
  assumptionActionText: { color: colors.textAccent, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  assumptionArrow: { color: colors.textAccent, fontSize: 22, lineHeight: 24 },
  sectionLabel: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  changeReceipt: { backgroundColor: colors.positiveSoft, borderBottomLeftRadius: radius.control, borderRadius: radius.sheet, flex: 1, gap: spacing[1], padding: spacing[3] },
  changeTitle: { color: colors.positive, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  changeLine: { color: colors.text, fontSize: type.helper, lineHeight: type.helperLine },
  artifact: { backgroundColor: colors.surface, borderColor: colors.border, borderRadius: radius.sheet, borderWidth: StyleSheet.hairlineWidth, marginTop: spacing[5], overflow: "hidden" },
  artifactBody: { padding: spacing[4] },
  planSummary: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.overlay, gap: spacing[4], marginTop: spacing[3], padding: spacing[4] },
  planHeader: { alignItems: "center", flexDirection: "row", gap: spacing[3], justifyContent: "space-between" },
  planMeta: { color: colors.text, fontSize: type.body, fontWeight: "500", lineHeight: type.bodyLine, marginTop: spacing[1] },
  totalRow: { alignItems: "flex-end", flexDirection: "row", justifyContent: "space-between" },
  totalLabel: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  total: { color: colors.text, fontSize: type.price, fontWeight: "600", lineHeight: type.priceLine, marginTop: spacing[1] },
  budgetColumn: { alignItems: "flex-end" },
  budgetAmount: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine, marginTop: spacing[1] },
  remainingPill: { alignSelf: "flex-start", backgroundColor: colors.positiveSoft, borderRadius: radius.pill, paddingHorizontal: spacing[3], paddingVertical: spacing[1] },
  remaining: { color: colors.positive, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  overBudgetPill: { alignSelf: "flex-start", backgroundColor: colors.dangerSoft, borderRadius: radius.pill, paddingHorizontal: spacing[3], paddingVertical: spacing[1] },
  overBudgetText: { color: colors.danger, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  comparisonWarning: { backgroundColor: colors.warningSoft, borderRadius: radius.overlay, padding: spacing[3] },
  comparisonWarningText: { color: colors.warning, fontSize: type.helper, lineHeight: type.helperLine },
  resultsHeader: { alignItems: "center", flexDirection: "row", gap: spacing[2], justifyContent: "space-between", marginTop: spacing[6] },
  sectionTitle: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  resultCount: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, marginTop: spacing[1] },
  candidates: { gap: spacing[4], marginTop: spacing[4] },
  candidateStack: { gap: spacing[2] },
  resultsPending: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, paddingVertical: spacing[5], textAlign: "center" },
  resultsEmpty: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, paddingVertical: spacing[5], textAlign: "center" },
  collapsedResults: { alignItems: "center", backgroundColor: colors.surfaceSubtle, borderRadius: radius.overlay, flexDirection: "row", gap: spacing[3], marginTop: spacing[4], minHeight: 76, padding: spacing[3] },
  collapsedVisuals: { flexDirection: "row" },
  collapsedDot: { alignItems: "center", backgroundColor: colors.surfaceSelected, borderColor: colors.surface, borderRadius: radius.pill, borderWidth: 2, height: 42, justifyContent: "center", marginRight: -8, width: 42 },
  collapsedDotText: { color: colors.textAccent, fontSize: 13, fontWeight: "600" },
  collapsedText: { color: colors.text, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  destinationPanel: { marginTop: spacing[5] },
  backgroundPanel: { marginTop: spacing[5] },
  composerDock: { alignItems: "center", backgroundColor: colors.surface, borderTopColor: colors.border, borderTopWidth: StyleSheet.hairlineWidth, paddingHorizontal: spacing[3], paddingTop: spacing[3] },
  composerDockInner: { gap: spacing[2], maxWidth: size.contentMax, width: "100%" },
  followUpHeader: { gap: spacing[2] },
  followUpTitle: { color: colors.textMuted, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  followUpChips: { flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  followUpChip: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.pill, justifyContent: "center", minHeight: 40, paddingHorizontal: spacing[3] },
  followUpChipText: { color: colors.textAccent, fontSize: 12, fontWeight: "500" },
  error: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine },
  pressed: { opacity: 0.58 },
});
