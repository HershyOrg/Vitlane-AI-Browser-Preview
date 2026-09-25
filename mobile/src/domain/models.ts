export type Locale = "ko-KR" | "en-US";

export type LocalizedText = Readonly<Record<Locale, string>>;

export type Currency = "KRW" | "USD";

export interface Money {
  readonly currency: Currency;
  readonly amount: number;
}

/**
 * A candidate price is an observation, not a defaultable number. Keep the
 * three states aligned with the Web candidate presentation contract so a
 * missing provider observation can never be rendered or compared as zero.
 */
export type CandidatePrice =
  | { readonly kind: "OBSERVED"; readonly amount: Money }
  | { readonly kind: "RANGE"; readonly minimum: Money; readonly maximum: Money }
  | { readonly kind: "UNKNOWN"; readonly reasonCode?: string };

export interface CandidateProvenance {
  readonly source: string;
  readonly apiProvider?: string;
  readonly apiProduct?: string;
  readonly discoveryChannel?: string;
  readonly observedAt?: string;
  readonly productUrl?: string;
  readonly purchaseRoute?: "VITLANE_CHECKOUT" | "EXTERNAL";
  readonly hydrationStatus?: "READY" | "UNRESOLVED" | "FAILED";
  readonly availability?: "AVAILABLE" | "UNAVAILABLE" | "UNKNOWN";
}

export type PlanItemState = "selected" | "already_owned" | "removed";
export type PlanItemKind = "product" | "service";

export interface ServiceSlot {
  readonly id: string;
  readonly start: string;
  readonly end: string;
  readonly timeZone: "Asia/Seoul";
  readonly state: "known";
}

export interface PlanItemView {
  readonly id: string;
  readonly kind: PlanItemKind;
  readonly title: LocalizedText;
  readonly merchantId: string | null;
  readonly merchantLabel: LocalizedText | null;
  readonly quantity: number;
  readonly unitPrice: Money;
  readonly necessity: "required" | "optional";
  readonly state: PlanItemState;
  readonly slot?: ServiceSlot;
  readonly partySize?: number;
  readonly locationLabel?: LocalizedText;
  readonly cancellationSummary?: LocalizedText;
}

export interface PlanTotals {
  readonly subtotal: Money;
  readonly knownShipping: Money;
  readonly knownTotal: Money;
  readonly totalState: "COMPLETE" | "PARTIAL";
  readonly hasUnknownFees: boolean;
  readonly unknownMerchantIds: readonly string[];
  readonly budget: Money;
  readonly remainingBudget: Money;
  readonly overBudget: Money;
  readonly budgetComparisonState: "AVAILABLE" | "INCOMPLETE" | "CURRENCY_MISMATCH";
  readonly budgetState: "LIMITED" | "UNLIMITED";
}

export interface PlanView {
  readonly id: string;
  readonly revision: number;
  readonly items: readonly PlanItemView[];
  readonly shippingByMerchant: Readonly<Record<string, number | null>>;
  readonly totals: PlanTotals;
}

export interface ConditionChipView {
  readonly id: string;
  readonly label: LocalizedText;
  readonly value: LocalizedText;
  readonly confirmed: boolean;
  readonly reason?: LocalizedText;
  readonly refinementQuestionId?: string;
}

export interface ChoiceOptionView {
  readonly id: string;
  readonly title: LocalizedText;
  readonly description: LocalizedText;
}

export interface QuestionView {
  readonly id: string;
  readonly revision: number;
  readonly title: LocalizedText;
  readonly reason: LocalizedText;
  readonly selection: "single";
  readonly optional: boolean;
  readonly allowCustom: boolean;
  readonly options: readonly ChoiceOptionView[];
  readonly state: "unanswered" | "answered";
  readonly selectedOptionIds: readonly string[];
  readonly customText?: string;
  readonly answerOrigin?: "assumed" | "user";
}

export interface QuestionAnswerInput {
  readonly questionId: string;
  readonly selectedOptionIds: readonly string[];
  readonly customText?: string;
  readonly disposition: "apply" | "no_preference";
}

export interface CandidateView {
  readonly id: string;
  readonly title: LocalizedText;
  readonly description: LocalizedText;
  readonly badge: LocalizedText;
  readonly price: CandidatePrice;
  readonly itemIds: readonly string[];
  readonly tone: "sage" | "sand" | "mint" | "lavender";
  readonly fitReasons: readonly LocalizedText[];
  readonly imageUrl?: string;
  readonly imageAlt?: LocalizedText;
  readonly provenance: CandidateProvenance;
}

export interface CommittedQuestionAnswer {
  readonly questionId: string;
  readonly selectedOptionIds: readonly string[];
  readonly customText?: string;
  readonly committedAtRevision: number;
  readonly origin: "assumed" | "user";
}

export interface FollowUpSuggestionView {
  readonly id: string;
  readonly text: LocalizedText;
}

/** Exact, count-based progress for one shopping goal. No synthetic percentage is inferred. */
export interface ShoppingGoalView {
  readonly title: LocalizedText;
  readonly status: "working" | "needs_input" | "ready" | "review_required" | "failed";
  readonly activeTargetCount: number;
  readonly candidateCount: number;
  readonly selectedCount: number;
  readonly unresolvedQuestionCount: number;
  readonly coverage: "NONE" | "PARTIAL" | "COMPLETE";
}

export interface WorkspaceActivityView {
  readonly id: string;
  readonly kind: "request" | "result" | "work" | "message";
  readonly label: LocalizedText;
  /** User or server-authored display text is preserved in its source language. */
  readonly body?: string;
  readonly occurredAt?: string;
  readonly status: "pending" | "running" | "complete" | "failed" | "cancelled" | "review_required";
}

export type AgentMessageStatus =
  | "PENDING"
  | "ACCEPTED"
  | "DISMISSED"
  | "ACKNOWLEDGED"
  | "SUPERSEDED";

export interface AgentMessageView {
  readonly id: string;
  readonly kind: "RESULT" | "PROPOSAL" | "ERROR" | "NOTICE" | "CLARIFICATION";
  readonly status: AgentMessageStatus;
  readonly version: number;
  readonly code: string;
  readonly title: LocalizedText;
  /** Generated content is immutable evidence and is not translated by the client. */
  readonly body: string;
  readonly contentLocale?: Locale;
  readonly targetTitle?: string;
  readonly availableAt?: string;
  readonly createdAt: string;
}

export type AgentMessageResponse = "ACCEPT" | "DISMISS" | "ACKNOWLEDGE";

export interface ResearchSubscriptionView {
  readonly id: string;
  readonly targetId: string;
  readonly targetTitle: string;
  readonly status: "ACTIVE" | "EXPIRED" | "CANCELLED" | "TARGET_REMOVED" | "ARCHIVED";
  readonly country: "KR" | "US";
  readonly currency: Currency;
  readonly maximum?: Money;
  readonly keywords: readonly string[];
  readonly expiresAt: string;
  readonly createdAt: string;
}

export interface ResearchFindingView {
  readonly id: string;
  readonly subscriptionId: string;
  readonly targetId: string;
  readonly status: "NEW" | "HIDDEN" | "ADDED";
  readonly title: string;
  readonly productUrl: string;
  readonly imageUrl?: string;
  readonly price: CandidatePrice;
  readonly provider: string;
  readonly reason: string;
  readonly observedAt: string;
  readonly createdAt: string;
  readonly candidateId?: string;
}

export interface BackgroundResearchView {
  readonly availability: "available" | "disabled";
  readonly subscriptions: readonly ResearchSubscriptionView[];
  readonly findings: readonly ResearchFindingView[];
}

export interface ShoppingPreferencesView {
  readonly availability: "available" | "disabled";
  readonly version: number;
  readonly effective: {
    readonly uiLocale: Locale;
    readonly preferredCurrency: Currency;
    readonly researchCountry: "KR" | "US";
  };
  readonly explicit: {
    readonly uiLocale?: Locale;
    readonly preferredCurrency?: Currency;
    readonly researchCountry?: "KR" | "US";
  };
}

export interface ShoppingPreferencesPatch {
  readonly uiLocale?: Locale;
  readonly preferredCurrency?: Currency;
  readonly researchCountry?: "KR" | "US";
}

export interface WorkspaceView {
  readonly id: string;
  readonly mode: "fixture" | "server";
  readonly title: LocalizedText;
  readonly intent: string;
  readonly revision: number;
  readonly notice: LocalizedText;
  readonly noticeTone?: "positive" | "warning" | "danger";
  readonly sourceLabel: LocalizedText;
  readonly conditions: readonly ConditionChipView[];
  readonly questions: readonly QuestionView[];
  readonly answers: readonly CommittedQuestionAnswer[];
  readonly activeQuestionId: string | null;
  readonly plan: PlanView;
  readonly budgetOptions: readonly Money[];
  readonly followUpSuggestions: readonly FollowUpSuggestionView[];
  readonly candidates: readonly CandidateView[];
  readonly activeCandidateIds: readonly string[];
  readonly selectedCandidateIds: readonly string[];
  readonly lastChange: readonly LocalizedText[];
  readonly goal?: ShoppingGoalView;
  readonly activity?: readonly WorkspaceActivityView[];
  readonly agentMessages?: readonly AgentMessageView[];
  readonly backgroundResearch?: BackgroundResearchView;
  readonly shoppingPreferences?: ShoppingPreferencesView;
  readonly references?: {
    readonly planId: string;
    readonly curationId: string;
  };
  readonly sourceCoverage?: readonly {
    readonly source: string;
    readonly status: "SUCCEEDED" | "EMPTY" | "PARTIAL" | "FAILED" | "UNSUPPORTED" | "SKIPPED";
    readonly candidateCount: number;
    readonly reasonCode?: string;
  }[];
  readonly activeWork?: {
    readonly status: "QUEUED" | "RUNNING" | "RESULT_CONFIRMATION_REQUIRED";
    readonly label: string;
    readonly detail?: string;
  };
  /** Canonical thread state used by polling and progressive disclosure. */
  readonly processing?: {
    readonly status:
      | "INTERPRETING"
      | "RUNNING"
      | "WAITING_SELECTION"
      | "RESULT_CONFIRMATION_REQUIRED"
      | "SUCCEEDED"
      | "FAILED"
      | "CANCELLED";
    readonly label: string;
    readonly detail?: string;
    readonly shouldPoll: boolean;
  };
}

export interface CommandContext {
  readonly idempotencyKey: string;
  readonly baseRevision: number;
}

export interface BrowserRunStartContext {
  readonly createIdempotencyKey: string;
  readonly navigationApprovalIdempotencyKey: string;
}

export type BrowserRunState =
  | "AWAITING_NAVIGATION_APPROVAL"
  | "NAVIGATION_APPROVED"
  | "AWAITING_PREPARATION_APPROVAL"
  | "PREPARATION_APPROVED"
  | "USER_CONTROL"
  | "RESUME_REQUIRES_OBSERVATION"
  | "PAUSED"
  | "READY_FOR_USER_PAYMENT"
  | "COMPLETED"
  | "PARTIAL"
  | "FAILED"
  | "RESULT_UNKNOWN"
  | "CANCELLED";

/**
 * Server-owned navigation grant for one stored candidate. `productUrl` is
 * returned by the BrowserRun API; callers must never substitute a client URL.
 */
export interface BrowserRunView {
  readonly id: string;
  readonly curationId: string;
  readonly candidateId: string;
  readonly productUrl: string;
  readonly merchantOrigin: string;
  readonly merchantHost: string;
  readonly state: BrowserRunState;
  readonly controlOwner: "NONE" | "AGENT" | "USER";
  readonly version: number;
}

export interface BudgetChangePreview {
  readonly id: string;
  readonly workspaceId: string;
  readonly baseRevision: number;
  readonly proposedRevision: number;
  readonly requestedBudget: Money;
  readonly before: PlanTotals;
  readonly projectedPlan: PlanView;
  readonly changes: readonly {
    readonly itemId: string;
    readonly before: LocalizedText;
    readonly after: LocalizedText;
    readonly reason: LocalizedText;
  }[];
}

export class FixtureDomainError extends Error {
  public constructor(
    public readonly code:
      | "NOT_FOUND"
      | "REVISION_CONFLICT"
      | "IDEMPOTENCY_CONFLICT"
      | "INVALID_ANSWER"
      | "UNSUPPORTED_FIXTURE_CHANGE",
    message: string,
  ) {
    super(message);
    this.name = "FixtureDomainError";
  }
}
