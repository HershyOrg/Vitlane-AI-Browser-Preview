// shared/openapi/v1.yaml을 기준으로 사용하는 Web 공유 타입이다.
export type Money = {
  amount: string;
  currency: string;
};

export type LocationContext = {
  country: string;
  city?: string;
};

export type URLMode = "NONE" | "REFERENCE" | "EXACT_PRODUCT";
export type ExecutionMode = "EXPERIMENT" | "LIVE";
export type PlanningMode = "SINGLE" | "AUTO";

export type ResearchScope = {
  category?: string;
  country: string;
  city?: string;
  allowedItems: string[];
  blockedItems: string[];
  minPrice?: Money | null;
  maxPrice?: Money | null;
  referenceUrl?: string;
  urlMode: URLMode;
};

export type ShoppingPlan = {
  id: string;
  userId: string;
  originalIntent: string;
  planningMode: PlanningMode;
  executionMode: ExecutionMode;
  totalBudget: Money;
  locationContext: LocationContext;
  researchScope: ResearchScope;
  /**
   * Which provider executes this plan's intelligence work, fixed at
   * submission. Older preview plans predate the field, so it is optional and
   * absent means the retired EXTERNAL mode.
   */
  agentMode?: "MANAGED" | "EXTERNAL";
  modelKey?: string;
  createdAt: string;
};

export type Curation = {
  id: string;
  shoppingPlanId: string;
  userId: string;
  phase: "PLANNING" | "CURATING";
  version: number;
  createdAt: string;
  updatedAt: string;
  archivedAt?: string;
  archivedByUserId?: string;
};

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

export type CurationActionDescriptor = {
  id: CurationActionType;
  alias:
    | "@Intent-NextStep"
    | "@TargetList-AddTarget"
    | "@Planning-NextStep"
    | "@Curation-AddTarget"
    | "@Target-ResearchAgain"
    | "@Target-Remove"
    | "@Selection-Mutation";
  enabled: boolean;
  unavailableReason?: string;
  subjectSchema: {
    type: CurationActionSubjectType;
    idRequired: boolean;
  };
  bodySchema:
    | "NONE"
    | "TEXT_REQUIRED"
    | "TEXT_OPTIONAL"
    | "TYPED_COMMAND";
  effectKind: "NONE" | "INTELLIGENCE";
  transcriptPolicy: "APPEND" | "PATCH_ONLY";
  expectedResourceVersion: number;
  requiresConfirmation: boolean;
  requestedTransitionTo?: Curation["phase"];
};

export type CurationAction = {
  id: string;
  curationId: string;
  actorUserId: string;
  type: CurationActionType;
  phaseAtRequest: "HAVING_INTENT" | Curation["phase"];
  requestedTransitionTo?: Curation["phase"];
  subjectType: CurationActionSubjectType;
  subjectId?: string;
  effectKind: CurationActionDescriptor["effectKind"];
  sourceRefType:
    | "SHOPPING_PLAN"
    | "CURATION_RUN_REQUEST"
    | "RESEARCH_START_REQUEST"
    | "RESEARCH_AGAIN_REQUEST"
    | "PLAN_TARGET"
    | "CURATION_SELECTION_COMMAND";
  sourceRefId: string;
  expectedCurationVersion: number;
  createdAt: string;
};

export type CurationTimelineAction = Omit<
  CurationAction,
  "actorUserId" | "sourceRefType" | "sourceRefId"
>;

export type CurationTimelineItem = {
  action: CurationTimelineAction;
  displayBody?: string;
  result?: {
    kind:
      | "INTENT_ACCEPTED"
      | "TARGET_EXPANSION"
      | "RESEARCH_STARTED"
      | "TARGET_RESEARCHED";
    summary: string;
    occurredAt: string;
    diff?: {
      added?: string[];
      changed?: string[];
      removed?: string[];
    };
  };
};

export type AvailableCurationActions = {
  curationId: string;
  phase: Curation["phase"];
  version: number;
  availableActions: CurationActionDescriptor[];
};

export type PlanTarget = {
  productVertical?: "GENERAL" | "FASHION" | "BEAUTY" | "FOOD" | "LIVING" | "ELECTRONICS";
  id: string;
  planId: string;
  curationId: string;
  userId: string;
  title: string;
  normalizedIntent: string;
  category?: string;
  allocatedBudget: Money;
  researchScope: ResearchScope;
  orderIndex: number;
  confirmedAt?: string;
  targetHash?: string;
  targetHashSchema: "vitlane.plan-target.v1";
  createdByCurationRunId?: string;
  removedAt?: string;
  removedByUserId?: string;
  version: number;
  createdAt: string;
  updatedAt: string;
};

export type PlanResult = {
  plan: ShoppingPlan;
  curation: Curation;
  targets: PlanTarget[];
  sessions: ShoppingSession[];
  planningTask?: PlanningTask;
  intelligenceJob?: { jobId: string; replay: boolean };
  availableActions: CurationActionDescriptor[];
  journey: PlanJourney;
};

export type JourneyStepKey = "INTENT" | "CURATION";

export type JourneyStep = {
  key: JourneyStepKey;
  label: string;
  state: "COMPLETED" | "CURRENT" | "LOCKED";
  path?: string;
  readOnly: boolean;
};

export type PlanJourney = {
  currentStep: JourneyStepKey;
  currentStage:
    | "PLANNING"
    | "READY"
    | "RESEARCHING"
    | "REVIEWING"
    | "CURATING";
  resumePath: string;
  nextAction: string;
  steps: JourneyStep[];
  sessionCount: {
    ready: number;
    researching: number;
    reviewing: number;
  };
};

export type PlanningTask = {
  id: string;
  planId: string;
  userId: string;
  contextVersion: number;
  contextHash: string;
  status: "REQUESTED" | "COMPLETED" | "CANCELLED";
  expiresAt: string;
  completedAt?: string;
  firstDiscoveredAt?: string;
  firstContextReadAt?: string;
  lastAgentActivityAt?: string;
  createdAt: string;
  updatedAt: string;
};

export type CurationRun = {
  id: string;
  curationId: string;
  planId: string;
  userId: string;
  kind: "INITIAL" | "EXPANSION";
  instruction: string;
  planningTaskId: string;
  status:
    | "REQUESTED"
    | "MATERIALIZING"
    | "COMPLETED"
    | "FAILED"
    | "CANCELLED";
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
};

export type ExpansionResult = {
  run: CurationRun;
  planningTask: PlanningTask;
  intelligenceJob?: { jobId: string; replay: boolean };
  replay: boolean;
};

export type TargetSnapshot = {
  id: string;
  curationId: string;
  planId: string;
  title: string;
  normalizedIntent: string;
  category: string;
  allocatedBudget: Money;
  targetHash: string;
  targetHashSchema: string;
  confirmedAt: string;
};

export type ShoppingSession = {
  id: string;
  planTargetId: string;
  userId: string;
  targetSnapshot: TargetSnapshot;
  researchScopeSnapshot: ResearchScope;
  status: "READY" | "RESEARCHING" | "REVIEWING";
  currentResearchRoundId?: string;
  version: number;
  createdAt: string;
  updatedAt: string;
};

export type CurrentUser = {
  analyticsUserId?: string;
  id: string;
  email: string;
  displayName: string;
  createdAt: string;
  marketingAdmin: boolean;
  phase5Operator: boolean;
};

export type ResearchRound = {
  id: string;
  shoppingSessionId: string;
  userId: string;
  roundNumber: number;
  contextSchema: "vitlane.research-context.v1";
  contextVersion: number;
  contextHash: string;
  status:
    | "REQUESTED"
    | "RESULTS_READY"
    | "NO_RESULTS"
    | "FAILED"
    | "CANCELLED"
    | "SUPERSEDED";
  failureReasonCode?: string;
  failureRetryable?: boolean;
  firstDiscoveredAt?: string;
  firstContextReadAt?: string;
  lastAgentActivityAt?: string;
  createdAt: string;
  completedAt?: string;
};

export type VariantInputKind = "ENUM" | "ENUM_OR_VALUE" | "VALUE";

export type VariantDiscoveryStatus =
  | "NOT_APPLICABLE"
  | "OBSERVED_PARTIAL"
  | "COMPLETE_UNVERIFIED"
  | "PROVIDER_VERIFIED"
  | "UNKNOWN";

export type VariantField = {
  key: string;
  label: string;
  inputKind: VariantInputKind;
  required: boolean;
  knownValues: Array<{
    value: string;
    label: string;
  }>;
  source:
    | "AGENT_OBSERVATION"
    | "PROVIDER"
    | "USER";
  discoveryStatus?: VariantDiscoveryStatus;
};

export type VariantDiscovery = {
  schemaVersion?: "vitlane.variant-discovery.v1";
  status: VariantDiscoveryStatus;
  fields: VariantField[];
  providerVariantRefs?: Array<{
    provider: string;
    schemaVersion: string;
    sellerId?: string;
    productId?: string;
    variantId?: string;
    offerId?: string;
    providerDetailHash?: string;
  }>;
  observedAt: string;
  evidence?: {
    summary?: string;
    sourceUrls?: string[];
    evidenceHash?: string;
  };
};

export type Orderability = {
  providerKind: string;
  executionMode: "MANUAL_MERCHANT_ORDER";
  externalEffect: "SIMULATED";
  liveOrderability: "UNVERIFIED" | "VERIFIED";
  settlementStatus?: "SUPPORTED" | "UNSUPPORTED";
  status:
    | "TEST_ORDER_FLOW_AVAILABLE"
    | "OPTIONS_REQUIRED"
    | "PRICE_RECHECK_REQUIRED"
    | "SETTLEMENT_CURRENCY_UNSUPPORTED"
    | "POLICY_BLOCKED";
  reasonCodes?: string[];
};

export type CandidateConfigurationInput = {
  fields: VariantField[];
  selections: Record<string, string>;
  confirmsNoOptions: boolean;
};

export type CandidateEvidence = {
  summary: string;
  matchedCriteria: string[];
  tradeoffs: string[];
  sourceUrls: string[];
};

export type Candidate = {
  id: string;
  researchSubmissionId: string;
  shoppingSessionId: string;
  productUrl: string;
  merchantDomain: string;
  category: string;
  name: string;
  description: string;
  imageUrl?: string;
  price: Money;
  variantDiscovery: VariantDiscovery;
  orderability: Orderability;
  evidence: CandidateEvidence;
  observedAt: string;
  orderSupport: "UNKNOWN" | "SUPPORTED" | "UNSUPPORTED";
  eligibility: {
    hardChecks: "PASS";
    semanticReview: "REQUIRED";
    reasonCodes: string[];
    policyVersion: string;
  };
  candidateHashSchema: "vitlane.candidate.v3";
  candidateHash: string;
  orderIndex: number;
  createdAt: string;
};

export type ResearchGroup = {
  session: ShoppingSession;
  round?: ResearchRound;
  rounds: ResearchRound[];
};

export type PlanResearchResult = {
  planId: string;
  groups: ResearchGroup[];
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

export type CartView = {
  curationId: string;
  selections: Array<{
    selection: CurationSelection;
    candidate: {
      id: string;
      name: string;
      productUrl: string;
      imageUrl?: string;
    };
    unitPrice: Money;
    lineTotal: Money;
  }>;
  total: Money;
  warnings: Array<{
    code: string;
    selectionId?: string;
    message: string;
  }>;
  updatedAt: string;
};

export type CreateCurationSelectionRequest = {
  clientCommandId: string;
  candidateConfigurationId: string;
  expectedCurationVersion: number;
  quantity: number;
};

export type UpdateCurationSelectionRequest =
  CreateCurationSelectionRequest & {
    expectedVersion: number;
  };

export type RemoveCurationSelectionRequest = {
  clientCommandId: string;
  expectedVersion: number;
  expectedCurationVersion: number;
};

export type CurationSelectionCommandResult = {
  selection: CurationSelection;
  replay: boolean;
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

export type CurationWorkspace = {
  plan: ShoppingPlan;
  curation: Curation;
  targets: PlanTarget[];
  research: PlanResearchResult;
  cart: CartView;
  availableActions: CurationActionDescriptor[];
  timeline: CurationTimelineItem[];
  latestArtifact: "TARGET_LIST" | "CURATION_BOARD";
  coverage: "NONE" | "PARTIAL" | "COMPLETE";
  agencyOrderTrace: CurationAgencyOrderTrace[];
  activeWork?: {
    workTargetId: string;
    label: string;
    status: "QUEUED" | "RUNNING" | "RESULT_CONFIRMATION_REQUIRED";
    detail?: string;
  };
};

export type ResearchAgainResult = {
  session: ShoppingSession;
  round: ResearchRound;
  feedback: {
    id: string;
    shoppingSessionId: string;
    previousRoundId: string;
    nextRoundId: string;
    feedback: string;
    schemaVersion: "vitlane.research-feedback.v2";
    feedbackVersion: number;
    feedbackHash: string;
    status: "ACTIVE" | "CANCELLED";
    createdAt: string;
    cancelledAt?: string;
  };
  replay: boolean;
};

export type APIErrorPayload = {
  error: {
    code: string;
    message: string;
    reasonCode?: string;
    retryable?: boolean;
    resource?: string;
    retryAfter?: string;
    agencyOrderId?: string;
    itemTitle?: string;
  };
};
