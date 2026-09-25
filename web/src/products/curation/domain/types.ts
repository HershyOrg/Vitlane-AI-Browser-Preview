import type {
  CandidateConfigurationInput,
  ExpansionResult,
  PlanResearchResult,
  PlanTarget,
  ShoppingPlan,
  VariantField,
} from "../../../shared/api/types";

export type Money = {
  amount: string;
  currency: string;
};

/**
 * HAVING_INTENT is a workspace/action context. Persisted Curation aggregates
 * use PLANNING or CURATING after the initial intent command creates them.
 */
export type CurationPhase = "HAVING_INTENT" | "PLANNING" | "CURATING";

export type CurationActionType =
  | "INTENT_NEXT_STEP"
  | "PLANNING_ADD_TARGETS"
  | "PLANNING_START_CURATING"
  | "CURATION_ADD_TARGETS"
  | "TARGET_RESEARCH_AGAIN"
  | "TARGET_REMOVE"
  | "SELECTION_MUTATION";

export type CurationActionSubjectType =
  | "INTENT"
  | "TARGET_LIST"
  | "CURATION"
  | "TARGET"
  | "SELECTION";

export type CurationActionBodySchema =
  | "NONE"
  | "TEXT_REQUIRED"
    | "TEXT_OPTIONAL"
  | "TYPED_COMMAND";

export type CurationActionEffectKind =
  | "NONE"
  | "INTELLIGENCE";

export type CurationActionTranscriptPolicy = "APPEND" | "PATCH_ONLY";

export type CurationActionStep = {
  id: string;
  label: string;
  actor: "USER" | "VITLANE" | "AGENT";
  status?: "PENDING" | "ACTIVE" | "COMPLETED" | "FAILED";
};

export type CurationActionDescriptor = {
  id: CurationActionType;
  alias: `@${string}-${string}`;
  enabled: boolean;
  unavailableReason?: string;
  subjectSchema: {
    type: CurationActionSubjectType;
    idRequired: boolean;
  };
  bodySchema: CurationActionBodySchema;
  effectKind: CurationActionEffectKind;
  transcriptPolicy: CurationActionTranscriptPolicy;
  expectedResourceVersion: number;
  requiresConfirmation: boolean;
  requestedTransitionTo?: "PLANNING" | "CURATING";
  steps?: CurationActionStep[];
};

export type CurationAction = {
  id: string;
  curationId: string;
  actorUserId: string;
  type: CurationActionType;
  phaseAtRequest: CurationPhase;
  requestedTransitionTo?: "PLANNING" | "CURATING";
  subjectType: CurationActionSubjectType;
  subjectId?: string;
  effectKind: CurationActionEffectKind;
  sourceRefType: string;
  sourceRefId: string;
  expectedCurationVersion: number;
  createdAt: string;
};

export type CurationTimelineAction = Omit<
  CurationAction,
  "actorUserId" | "sourceRefType" | "sourceRefId"
>;

export type CurationTimelineResultKind =
  | "INTENT_ACCEPTED"
  | "TARGET_EXPANSION"
  | "RESEARCH_STARTED"
  | "TARGET_RESEARCHED";

export type CurationTimelineResult = {
  kind: CurationTimelineResultKind;
  summary: string;
  occurredAt: string;
  diff?: CurationArtifactDiff;
};

export type CurationTimelineItem = {
  action: CurationTimelineAction;
  displayBody?: string;
  result?: CurationTimelineResult;
};

export type AvailableCurationActions = {
  curationId: string;
  phase: "PLANNING" | "CURATING";
  version: number;
  availableActions: CurationActionDescriptor[];
};

export type CurationSelection = {
  id: string;
  curationId: string;
  targetId: string;
  shoppingSessionId: string;
  candidateId: string;
  candidateConfigurationId: string;
  configurationHash: string;
  quantity: number;
  version: number;
  selectedAt: string;
  updatedAt: string;
  removedAt?: string;
};

export type CartViewCandidate = {
  id: string;
  name: string;
  productUrl: string;
  imageUrl?: string;
};

export type CartSelection = {
  selection: CurationSelection;
  candidate: CartViewCandidate;
  unitPrice: Money;
  lineTotal: Money;
};

export type CartWarning = {
  code: string;
  selectionId?: string;
  message: string;
};

/**
 * CartView is a state-free projection of active Selection records. In
 * particular, it deliberately has no OPEN/CHECKED_OUT lifecycle field.
 */
export type CartView = {
  curationId: string;
  selections: CartSelection[];
  total: Money;
  warnings: CartWarning[];
  updatedAt: string;
};

export type SelectionCommandResult = {
  selection: CurationSelection;
  replay: boolean;
};

export type TargetRemoveActionResult = {
  action: CurationAction;
  targetId: string;
  curationVersion: number;
  removedAt: string;
  replay: boolean;
};

export type CreateSelectionInput = {
  clientCommandId: string;
  candidateConfigurationId: string;
  expectedCurationVersion: number;
  quantity: number;
};

export type UpdateSelectionInput = CreateSelectionInput & {
  expectedVersion: number;
};

export type RemoveSelectionInput = {
  clientCommandId: string;
  expectedCurationVersion: number;
  expectedVersion: number;
};

export type PrepareCandidateConfigurationInput =
  CandidateConfigurationInput;

export type CandidateConfigurationSelection = {
  key: string;
  label: string;
  value: string;
  valueLabel: string;
  source: "USER_CONFIRMED" | "USER";
};

export type CandidateConfiguration = {
  id: string;
  configurationSequence: number;
  shoppingSessionId: string;
  candidateId: string;
  userId: string;
  schemaVersion: "vitlane.candidate-configuration.v1";
  fields: VariantField[];
  selections: CandidateConfigurationSelection[];
  confirmsNoOptions: boolean;
  configurationHash: string;
  createdAt: string;
};

export type PrepareCandidateConfigurationResponse = {
  configuration: CandidateConfiguration;
};

export type CurationConflictErrorCode =
  | "CURATION_SELECTION_VERSION_CONFLICT"
  | "CURATION_SELECTION_COMMAND_CONFLICT"
  | "VERSION_CONFLICT"
  | "IDEMPOTENCY_KEY_REUSED"
  | "CURATION_ACTION_UNAVAILABLE"
  | "CURATION_ACTION_IN_PROGRESS"
  | "CURATION_ARCHIVED";

export type CurationCoverage = "NONE" | "PARTIAL" | "COMPLETE";

export type CurationRailItem = {
  id: string;
  title: string;
  phase: CurationPhase;
  coverage: CurationCoverage;
  updatedAt: string;
  activeWork?: boolean;
};

export type CurationArtifactDiff = {
  added?: string[];
  changed?: string[];
  removed?: string[];
};

export type CurationArtifactTarget = {
  id: string;
  title: string;
  candidateCount?: number;
  status?: "READY" | "RESEARCHING" | "REVIEWING";
};

export type CurationArtifact = {
  id: string;
  kind: "INTENT" | "TARGET_LIST" | "CURATION";
  title: string;
  summary?: string;
  updatedAt: string;
  diff?: CurationArtifactDiff;
  targets?: CurationArtifactTarget[];
};

export type CurationTimelineEntry =
  | {
      id: string;
      kind: "ACTION";
      createdAt: string;
      command: string;
      action?: CurationTimelineAction;
    }
  | {
      id: string;
      kind: "RESULT";
      createdAt: string;
      summary: string;
      artifact?: CurationArtifact;
    };

export type CurationPendingTranscriptAction = {
  id: string;
  command: string;
  createdAt: string;
};

export type CurationActiveWork = {
  workTargetId: string;
  label: string;
  status: "QUEUED" | "RUNNING" | "RESULT_CONFIRMATION_REQUIRED";
  detail?: string;
};

export type FollowUpMessage = {
 id: string; responseId: string; kind: "RESULT" | "PROPOSAL" | "ERROR" | "NOTICE" | "CLARIFICATION"; status: "PENDING" | "ACCEPTED" | "DISMISSED" | "ACKNOWLEDGED" | "SUPERSEDED"; version: number; createdAt: string; content: {code: string; body?: string; locale?: string; targetTitle?: string; added?: number; jobId?: string; reasonCode?: string; retryable?: boolean; availableAt?: string};
};
export type CurationConversation = {schemaVersion: "vitlane.curation-conversation.v1"; version: number; unfinished: boolean; messages: FollowUpMessage[]; requests: {id: string; body: string; mode: string; status: string; actionId?: string; createdAt: string}[]};

export type CurationWorkspaceModel = {
 conversation?: CurationConversation;
  curation: CurationRailItem & {
    version: number;
  };
  curations: CurationRailItem[];
  availableActions: CurationActionDescriptor[];
  timeline: CurationTimelineEntry[];
  cart: CartView;
  activeWork?: CurationActiveWork;
};

export type IntelligenceStep = {
  id: string;
  kind: "INTERPRETING" | "SEARCHING_CATALOG" | "RANKING" | "SUBMITTING";
  status: "RUNNING" | "SUCCEEDED" | "FAILED";
  reasonCode?: string;
  startedAt: string;
  completedAt?: string;
};

export type IntelligenceJob = {
  jobId: string;
  actionId: string;
  targetKind: "PLANNING_TASK" | "RESEARCH_ROUND";
  targetId: string;
  provider: string;
  status: "PENDING" | "RUNNING" | "SUCCEEDED" | "FAILED" | "CANCELLED";
  failureCode?: string;
  retryable: boolean;
  attempt: number;
  steps: IntelligenceStep[];
  /** Why a PENDING job is not running yet, when it may be claimed again, and how many of the user's research jobs are ahead of it. */
  queueReason?: string;
  notBefore?: string;
  queueAhead?: number;
};

export type CurationWorkspaceResponse = {
 conversation?: CurationConversation;
  plan: ShoppingPlan;
  curation: {
    id: string;
    shoppingPlanId: string;
    userId: string;
    phase: "PLANNING" | "CURATING";
    version: number;
    createdAt: string;
    updatedAt: string;
  };
  targets: PlanTarget[];
  research: PlanResearchResult;
  cart: CartView;
  availableActions: CurationActionDescriptor[];
  timeline: CurationTimelineItem[];
  latestArtifact: "TARGET_LIST" | "CURATION_BOARD";
  coverage?: CurationCoverage;
  agencyOrderTrace?: CurationAgencyOrderTrace[];
  activeWork?: CurationActiveWork;
  intelligence?: IntelligenceJob[];
  catalogResearch?: CatalogResearchWorkspaceResponse;
};

export type CatalogResearchCandidate = {
 source?: string;
 externalObservation?: import("./sourceProduct").ExternalProductObservation;
 sourceProductRef?: import("./sourceProduct").SourceProductRef;
  candidateId: string;
  title?: string;
  description?: string;
  priceMinimumMinor?: number;
  priceMaximumMinor?: number;
  currency?: string;
  mediaUrl?: string;
  mediaAlt?: string;
  categories?: string[];
  locator?: {
    kind: "PRODUCT_URL" | "MERCHANT_VARIANT";
    productUrl?: string;
    variantId?: string;
    sellerDomain?: string;
  };
  previewVariant?: {
    id: string;
    title: string;
    priceMinor: number;
    currency: string;
    available?: boolean;
    sellerName?: string;
    sellerDomain?: string;
  };
  axisAssessment?: import("./researchCriteria").AxisAssessment;
  intentPoint?: string;
  features: string[];
  specifications: string[];
};

export type CatalogResearchWorkspaceResponse = {
  schemaVersion: "vitlane.catalog-research-workspace.v1" | "vitlane.catalog-research-workspace.v3" | "vitlane.catalog-research-workspace.v4";
  pools: Array<{
    targetId: string;
    version: number;
    expandOrdinal: number;
    latestMode?: "REPLACE" | "APPEND";
    discoveryOutcome?: { schemaVersion: string; addedCount: number; duplicateCount: number; rejectedCount: number; evaluatedCount?: number; unevaluatedCount?: number; status: "ADDED" | "NO_NEW_CANDIDATES" | "EVALUATING" };
    sourceCoverage?: Array<{source: string;status: "SUCCEEDED" | "EMPTY" | "PARTIAL" | "FAILED" | "UNSUPPORTED";reasonCode?:string;candidateCount:number}> | null;
    latestDurationMilliseconds?: number;
    latestShopifyCalls?: number;
    latestRateRemaining?: number;
    products: CatalogResearchCandidate[];
    hiddenProducts: CatalogResearchCandidate[];
    messages?: [];
  }>;
  messages?: [];
  configurations: Array<{
    candidateId: string;
    // The saved option's version, so a card can save without reading it first.
    version?: number;
    variant: {
      variantId: string;
      title: string;
      priceMinor: number;
      currency: string;
      available: boolean;
      selectedOptions: Array<{ name: string; value: string }>;
      mediaUrl?: string;
      productUrl?: string;
    };
    observedAt: string;
  }>;
  interactions: Array<{
    candidateId: string;
    variantId: string;
    pinned: boolean;
    sentiment: "NONE" | "LIKE" | "DISLIKE";
  }>;
  // External card state travels with the workspace read, so a card grid asks
  // the server once for the whole curation instead of once per card.
  purchaseFeedback?: ExternalPurchaseFeedback;
  productReactions?: CatalogResearchProductReaction[];
  reactionAllowedCandidateIds?: string[];
};

export type ExternalPurchaseFeedback = {
  schemaVersion: string;
  version: number;
  records: Array<{
    candidateId: string;
    variantRef?: import("./sourceProduct").AmazonVariantRef;
    productRef?: import("./sourceProduct").KoreanProductRef;
    checked: boolean;
    version: number;
    recordedAt: string;
    evidence: "SELF_REPORTED";
  }>;
};

export type CatalogResearchProductReaction = {
  targetId: string;
  candidateId: string;
  productRef: import("./sourceProduct").KoreanProductRef;
  pinned: boolean;
  sentiment: "NONE" | "LIKE" | "DISLIKE";
  version: number;
  updatedAt: string;
};

export type CurationAgencyOrderTrace = {
  agencyOrderId: string;
  curationId: string;
  targetId: string;
  candidateId: string;
  state: string;
  issuedAt: string;
  updatedAt: string;
};

export type AddTargetsActionResult = {
  actionId: string;
  effect: ExpansionResult;
};

export type AutoResearchActionResult = {
  status: "EXECUTED" | "NEEDS_SELECTION" | "NO_ACTION";
  decision: "ADD_TARGET" | "RESEARCH_AGAIN" | "NEEDS_SELECTION" | "NO_ACTION" | "RETRY";
  source: "DETERMINISTIC" | "MANAGED";
  targetId?: string;
  reasonCode: string;
  replay: boolean;
};

export type ResearchAgentActionResult = {
  actionId: string;
  effect: {
    // Progress arrives through the workspace projection, so the browser holds
    // only the rounds the command opened.
    tasks: Array<{ roundId: string }>;
  };
  // A replayed command can resolve to a round that already finished. No new
  // work starts then, so the page must say so instead of looking inert.
  replayedTerminalRound?: boolean;
};

export type ParsedCurationCommand = {
  descriptor: CurationActionDescriptor;
  alias: CurationActionDescriptor["alias"];
  subjectId?: string;
  body: string;
};

export type CurationCommandSubmission = ParsedCurationCommand & {
  command: string;
};

export type CurationCommandSubject = {
  type: CurationActionSubjectType;
  id: string;
  label: string;
};

export type CurationCommandChoice = {
  key: string;
  descriptor: CurationActionDescriptor;
  alias: CurationActionDescriptor["alias"];
  subjectId?: string;
  subjectLabel?: string;
};
