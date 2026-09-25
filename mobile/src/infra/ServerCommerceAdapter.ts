import type { CommerceGateway } from "../commerce";
import type {
  AgentMessageResponse,
  AgentMessageView,
  BackgroundResearchView,
  BrowserRunStartContext,
  BrowserRunView,
  BudgetChangePreview,
  CandidatePrice,
  CandidateView,
  CommandContext,
  CommittedQuestionAnswer,
  ConditionChipView,
  Currency,
  LocalizedText,
  Money,
  PlanItemView,
  PlanTotals,
  PlanView,
  QuestionAnswerInput,
  QuestionView,
  ShoppingGoalView,
  ShoppingPreferencesPatch,
  ShoppingPreferencesView,
  WorkspaceActivityView,
  WorkspaceView,
} from "../domain";
import { moneyFromMinor } from "../domain";
import {
  type ManagedRunnerCapability,
  type ServerBackgroundResearch,
  type ServerBrowserRun,
  type ServerBrowserRunResponse,
  type ServerActiveWork,
  type ServerBudget,
  type ServerCatalogCandidate,
  type ServerCurationThread,
  type ServerHydratedCandidate,
  type ServerHydration,
  type ServerPlanResult,
  type ServerSourceCoverage,
  type ServerThreadAction,
  type ServerThreadList,
  type ServerThreadQuestion,
  type ServerWorkspace,
  type ServerUserPreferencesResult,
  VitlaneApiClient,
  VitlaneApiError,
} from "../api";

type ServerCommerceAdapterOptions = {
  api: VitlaneApiClient;
  country?: "KR" | "US";
  city?: string;
  currency?: Currency;
  executionMode?: "EXPERIMENT" | "LIVE";
  modelKey?: string;
  idFactory?: () => string;
  now?: () => number;
};

type LoadedState = {
  workspace: ServerWorkspace;
  budget: ServerBudget;
  threads: ServerCurationThread[];
  candidates: ServerHydratedCandidate[];
  hydrationIncomplete: boolean;
  candidateFingerprint: string;
  hydratedAtMilliseconds: number;
  backgroundResearch: ServerBackgroundResearch | null;
  preferences: ServerUserPreferencesResult | null;
};

type StoredPreview = {
  preview: BudgetChangePreview;
  curationId: string;
  budgetVersion: number;
  budgetEnabled: boolean;
  currency: Currency;
};

export class ServerCommerceError extends Error {
  public constructor(
    public readonly code:
      | "INVALID_INPUT"
      | "INVALID_SERVER_RESPONSE"
      | "MANAGED_RUNNER_UNAVAILABLE"
      | "NOT_FOUND"
      | "REVISION_CONFLICT",
    message: string,
  ) {
    super(message);
    this.name = "ServerCommerceError";
  }
}

/** Maps the canonical Vitlane API into the current mobile review view model. */
export class ServerCommerceAdapter implements CommerceGateway {
  public readonly mode = "server" as const;

  private readonly api: VitlaneApiClient;
  private readonly country: "KR" | "US";
  private readonly city?: string;
  private readonly currency: Currency;
  private readonly executionMode: "EXPERIMENT" | "LIVE";
  private readonly configuredModelKey?: string;
  private readonly idFactory: () => string;
  private readonly now: () => number;
  private readonly stateByCuration = new Map<string, LoadedState>();
  private readonly curationByPlan = new Map<string, string>();
  private readonly previewById = new Map<string, StoredPreview>();
  private readonly receiptByCuration = new Map<string, readonly LocalizedText[]>();
  private readonly commandIdAliases = new Map<string, string>();
  private preferencesCache?: {
    readonly value: ServerUserPreferencesResult | null;
    readonly readAtMilliseconds: number;
  };

  public constructor(options: ServerCommerceAdapterOptions) {
    this.api = options.api;
    this.country = options.country ?? "KR";
    this.city = options.city?.trim() || undefined;
    this.currency = options.currency ?? (this.country === "KR" ? "KRW" : "USD");
    this.executionMode = options.executionMode ?? "EXPERIMENT";
    this.configuredModelKey = options.modelKey?.trim() || undefined;
    this.idFactory = options.idFactory ?? secureUuidV4;
    this.now = options.now ?? Date.now;
  }

  public async createWorkspace(
    intent: string,
    context: CommandContext,
  ): Promise<WorkspaceView | null> {
    const normalizedIntent = intent.trim();
    if (!normalizedIntent) return null;
    if (context.baseRevision !== 0) {
      throw new ServerCommerceError("REVISION_CONFLICT", "A new workspace must start at revision zero");
    }
    const [modelKey, preferences] = await Promise.all([
      this.configuredModelKey
        ? Promise.resolve(this.configuredModelKey)
        : this.defaultManagedModelKey(),
      this.readOptionalPreferences(),
    ]);
    const country = preferences?.effective.researchCountry ?? this.country;
    const currency = preferences?.effective.preferredCurrency ?? this.currency;
    const city = country === this.country ? this.city : undefined;
    const idempotencyKey = this.commandId(context.idempotencyKey);
    const result = await this.api.request<ServerPlanResult>("/api/v1/shopping-plans", {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey },
      body: {
        originalIntent: normalizedIntent,
        planningMode: "AUTO",
        controlMode: "AUTO",
        budget: {
          schemaVersion: "vitlane.curation-budget.v1",
          inputMode: "AUTO",
          currency,
          totalAmount: null,
          allocationMode: "AUTO",
        },
        executionMode: this.executionMode,
        location: {
          country,
          ...(city ? { city } : {}),
        },
        agentMode: "MANAGED",
        modelKey,
      },
    });
    this.curationByPlan.set(result.plan.id, result.curation.id);
    this.receiptByCuration.set(result.curation.id, [localized(
      "요청을 받아 준비를 시작했어요.",
      "Your request was accepted and preparation has started.",
    )]);
    return this.getWorkspace(result.curation.id);
  }

  public async getWorkspace(workspaceId: string): Promise<WorkspaceView> {
    const state = await this.loadState(workspaceId);
    this.stateByCuration.set(workspaceId, state);
    this.curationByPlan.set(state.workspace.plan.id, workspaceId);
    return this.toWorkspaceView(state);
  }

  public async getPlan(planId: string): Promise<PlanView> {
    const knownCuration = this.curationByPlan.get(planId);
    if (knownCuration) return (await this.getWorkspace(knownCuration)).plan;
    const result = await this.api.request<ServerPlanResult>(
      `/api/v1/shopping-plans/${encodeURIComponent(planId)}`,
    );
    this.curationByPlan.set(planId, result.curation.id);
    return (await this.getWorkspace(result.curation.id)).plan;
  }

  public async getCandidates(workspaceId: string): Promise<readonly CandidateView[]> {
    return (await this.getWorkspace(workspaceId)).candidates;
  }

  public async startBrowserRun(
    workspaceId: string,
    candidateId: string,
    context: BrowserRunStartContext,
  ): Promise<BrowserRunView> {
    const curationId = workspaceId.trim();
    const storedCandidateId = candidateId.trim();
    if (!curationId || !storedCandidateId) {
      throw new ServerCommerceError("INVALID_INPUT", "Browser run candidate coordinates are required");
    }
    const base = `/api/v1/curations/${encodeURIComponent(curationId)}`
      + `/catalog-research/candidates/${encodeURIComponent(storedCandidateId)}/browser-runs`;
    const created = await this.api.request<ServerBrowserRunResponse>(base, {
      method: "POST",
      headers: { "Idempotency-Key": context.createIdempotencyKey },
      // The server resolves the URL from the owner-bound stored candidate.
      body: {},
    });
    let run = validateBrowserRunResponse(created, curationId, storedCandidateId);
    if (run.state === "AWAITING_NAVIGATION_APPROVAL") {
      const approved = await this.api.request<ServerBrowserRunResponse>(
        `/api/v1/browser-runs/${encodeURIComponent(run.id)}/navigation-approvals`,
        {
          method: "POST",
          headers: { "Idempotency-Key": context.navigationApprovalIdempotencyKey },
          body: { expectedVersion: run.version },
        },
      );
      run = validateBrowserRunResponse(approved, curationId, storedCandidateId);
    }
    if (!browserRunHasNavigationApproval(run)) {
      throw new ServerCommerceError(
        "REVISION_CONFLICT",
        "The candidate browser run is not approved for navigation",
      );
    }
    return browserRunView(run);
  }

  public async answerQuestion(
    workspaceId: string,
    answer: QuestionAnswerInput,
    _context: CommandContext,
  ): Promise<WorkspaceView> {
    const optionId = answer.selectedOptionIds[0];
    const text = answer.customText?.trim();
    if ((optionId ? 1 : 0) + (text ? 1 : 0) !== 1 || answer.selectedOptionIds.length > 1) {
      throw new ServerCommerceError("INVALID_INPUT", "Choose one option or enter one text answer");
    }
    const listed = await this.api.request<ServerThreadList>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/threads`,
    );
    const match = findQuestion(listed.threads, answer.questionId);
    if (!match) {
      throw new ServerCommerceError("NOT_FOUND", "The question is no longer waiting for an answer");
    }
    if (match.answer) {
      if (!serverAnswerMatches(match.answer, optionId, text)) {
        throw new ServerCommerceError("REVISION_CONFLICT", "The question was answered differently");
      }
      this.receiptByCuration.set(workspaceId, [localized(
        "이미 반영된 답변을 확인했어요.",
        "The previously submitted answer is already applied.",
      )]);
      return this.refreshAcceptedMutation(workspaceId, localized(
        "답변 반영 상태를 다시 불러오고 있어요.",
        "Refreshing the submitted answer.",
      ));
    }
    if (!match.waiting) {
      throw new ServerCommerceError("REVISION_CONFLICT", "The question is no longer waiting for an answer");
    }
    if (optionId && !(match.question.options ?? []).some((option) => option.id === optionId)) {
      throw new ServerCommerceError("INVALID_INPUT", "The selected option is not part of the current question");
    }
    await this.api.request<ServerCurationThread>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/threads/${encodeURIComponent(match.thread.id)}/answer`,
      {
        method: "POST",
        body: {
          revision: match.thread.revision,
          questionId: match.question.id,
          ...(optionId ? { optionId } : { text }),
        },
      },
    );
    this.receiptByCuration.set(workspaceId, [localized(
      "답변을 전달했어요. 변경 내용을 확인하고 있어요.",
      "Your answer was submitted and the changes are being checked.",
    )]);
    return this.refreshAcceptedMutation(workspaceId, localized(
      "답변 반영 상태를 다시 불러오고 있어요.",
      "Refreshing the submitted answer.",
    ));
  }

  /** Creates a local, non-authoritative diff. No retired proposal endpoint is called. */
  public async previewBudget(
    workspaceId: string,
    budgetAmount: number,
    context: CommandContext,
  ): Promise<BudgetChangePreview> {
    if (!Number.isFinite(budgetAmount) || budgetAmount <= 0) {
      throw new ServerCommerceError("INVALID_INPUT", "Budget must be a positive number");
    }
    const state = this.stateByCuration.get(workspaceId) ?? await this.loadAndRemember(workspaceId);
    if (state.workspace.curation.version !== context.baseRevision) {
      throw new ServerCommerceError("REVISION_CONFLICT", "The workspace changed before the budget preview was created");
    }
    const normalizedAmount = normalizeDisplayAmount(budgetAmount, state.budget.currency);
    const before = this.planTotals(state.workspace, state.budget);
    const requestedBudget = money(state.budget.currency, normalizedAmount);
    const comparisonAvailable = before.budgetComparisonState === "AVAILABLE"
      && before.knownTotal.currency === requestedBudget.currency;
    const projectedTotals: PlanTotals = {
      ...before,
      budget: requestedBudget,
      budgetState: "LIMITED",
      remainingBudget: money(
        state.budget.currency,
        comparisonAvailable ? Math.max(0, normalizedAmount - before.knownTotal.amount) : 0,
      ),
      overBudget: money(
        state.budget.currency,
        comparisonAvailable ? Math.max(0, before.knownTotal.amount - normalizedAmount) : 0,
      ),
      budgetComparisonState: comparisonAvailable
        ? "AVAILABLE"
        : before.budgetComparisonState === "CURRENCY_MISMATCH"
          ? "CURRENCY_MISMATCH"
          : "INCOMPLETE",
    };
    const preview: BudgetChangePreview = {
      id: `budget-preview:${this.idFactory()}`,
      workspaceId,
      baseRevision: state.workspace.curation.version,
      proposedRevision: state.workspace.curation.version,
      requestedBudget,
      before,
      projectedPlan: {
        ...this.planView(state.workspace, state.budget),
        totals: projectedTotals,
      },
      changes: [{
        itemId: "budget",
        before: localized(
          `예산 ${formatAmount(before.budget)}`,
          `Budget ${formatAmount(before.budget)}`,
        ),
        after: localized(
          `예산 ${formatAmount(requestedBudget)}`,
          `Budget ${formatAmount(requestedBudget)}`,
        ),
        reason: localized(
          state.budget.enabled
            ? "현재 항목 비율을 기준으로 배분할 예정이에요."
            : "각 항목에 같은 금액으로 배분할 예정이에요.",
          state.budget.enabled
            ? "The total will be allocated from the current target proportions."
            : "The total will be allocated equally across the current targets.",
        ),
      }],
    };
    this.previewById.set(preview.id, {
      preview,
      curationId: workspaceId,
      budgetVersion: state.budget.version,
      budgetEnabled: state.budget.enabled,
      currency: state.budget.currency,
    });
    return preview;
  }

  public async applyBudgetPreview(
    workspaceId: string,
    previewId: string,
    context: CommandContext,
  ): Promise<WorkspaceView> {
    const stored = this.previewById.get(previewId);
    if (!stored || stored.curationId !== workspaceId) {
      throw new ServerCommerceError("NOT_FOUND", "Budget preview was not found");
    }
    if (stored.preview.baseRevision !== context.baseRevision) {
      throw new ServerCommerceError("REVISION_CONFLICT", "The workspace changed after the budget preview");
    }
    const totalAmount = amountForServer(stored.preview.requestedBudget);
    await this.api.request<ServerBudget>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/budget`,
      {
        method: "PATCH",
        body: {
          schemaVersion: "vitlane.curation-budget.v1",
          commandId: this.commandId(context.idempotencyKey),
          expectedVersion: stored.budgetVersion,
          kind: stored.budgetEnabled ? "SET_TOTAL" : "ENABLE",
          currency: stored.currency,
          totalAmount,
          allocationMode: stored.budgetEnabled ? "PROPORTIONAL" : "EQUAL",
        },
      },
    );
    this.previewById.delete(previewId);
    this.receiptByCuration.set(workspaceId, [localized(
      `예산을 ${formatAmount(stored.preview.requestedBudget)}로 변경했어요.`,
      `Budget changed to ${formatAmount(stored.preview.requestedBudget)}.`,
    )]);
    return this.refreshAcceptedMutation(workspaceId, localized(
      "예산 변경 상태를 다시 불러오고 있어요.",
      "Refreshing the budget change.",
    ));
  }

  public async submitFollowUp(
    workspaceId: string,
    text: string,
    context: CommandContext,
  ): Promise<WorkspaceView> {
    const normalized = text.trim();
    if (!normalized) {
      throw new ServerCommerceError("INVALID_INPUT", "Follow-up text is required");
    }
    const clientRequestId = this.commandId(context.idempotencyKey);
    await this.api.request<ServerCurationThread>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/threads`,
      {
        method: "POST",
        headers: { "Idempotency-Key": clientRequestId },
        body: {
          clientRequestId,
          request: normalized,
          expectedCurationVersion: context.baseRevision,
        },
      },
    );
    this.receiptByCuration.set(workspaceId, [localized(
      "후속 요청을 전달했어요. 처리 상태를 확인하고 있어요.",
      "Your follow-up was submitted and its progress is being checked.",
    )]);
    return this.refreshAcceptedMutation(workspaceId, localized(
      "후속 요청 상태를 다시 불러오고 있어요.",
      "Refreshing the follow-up request.",
    ));
  }

  public async respondToAgentMessage(
    workspaceId: string,
    messageId: string,
    messageVersion: number,
    response: AgentMessageResponse,
    context: CommandContext,
  ): Promise<WorkspaceView> {
    if (!messageId.trim() || !Number.isInteger(messageVersion) || messageVersion < 1) {
      throw new ServerCommerceError("INVALID_INPUT", "A current agent message is required");
    }
    const clientRequestId = this.commandId(context.idempotencyKey);
    await this.api.request<{ status: "RECORDED" }>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/follow-ups/${encodeURIComponent(messageId)}/responses`,
      {
        method: "POST",
        headers: { "Idempotency-Key": clientRequestId },
        body: {
          response,
          expectedVersion: messageVersion,
          clientRequestId,
        },
      },
    );
    this.receiptByCuration.set(workspaceId, [localized(
      response === "ACCEPT"
        ? "제안을 수락했어요. 실제 반영 상태를 확인하고 있어요."
        : response === "DISMISS"
          ? "이 제안은 닫았어요."
          : "새 소식을 확인했어요.",
      response === "ACCEPT"
        ? "The proposal was accepted and its applied state is being checked."
        : response === "DISMISS"
          ? "The proposal was dismissed."
          : "The update was acknowledged.",
    )]);
    return this.refreshAcceptedMutation(workspaceId, localized(
      "제안 처리 상태를 다시 불러오고 있어요.",
      "Refreshing the proposal response.",
    ));
  }

  public async cancelResearchSubscription(
    workspaceId: string,
    subscriptionId: string,
  ): Promise<WorkspaceView> {
    await this.api.request<{ status: "RECORDED" }>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/subscriptions/${encodeURIComponent(subscriptionId)}/cancel`,
      { method: "POST" },
    );
    this.receiptByCuration.set(workspaceId, [localized(
      "이 조건은 더 이상 지켜보지 않아요.",
      "This saved deal condition is no longer being watched.",
    )]);
    return this.getWorkspace(workspaceId);
  }

  public async hideResearchFinding(
    workspaceId: string,
    findingId: string,
  ): Promise<WorkspaceView> {
    await this.api.request<{ status: "RECORDED" }>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/findings/${encodeURIComponent(findingId)}/hide`,
      { method: "POST" },
    );
    this.receiptByCuration.set(workspaceId, [localized(
      "발견한 상품을 숨겼어요.",
      "The discovered product was hidden.",
    )]);
    return this.getWorkspace(workspaceId);
  }

  public async importResearchFinding(
    workspaceId: string,
    findingId: string,
  ): Promise<WorkspaceView> {
    await this.api.request<{ candidateId: string }>(
      `/api/v1/curations/${encodeURIComponent(workspaceId)}/findings/${encodeURIComponent(findingId)}/candidates`,
      { method: "POST" },
    );
    this.receiptByCuration.set(workspaceId, [localized(
      "발견한 상품을 비교 후보에 추가했어요.",
      "The discovered product was added to the comparison candidates.",
    )]);
    return this.getWorkspace(workspaceId);
  }

  public async updateShoppingPreferences(
    workspaceId: string,
    patch: ShoppingPreferencesPatch,
  ): Promise<WorkspaceView> {
    if (!patch.uiLocale && !patch.preferredCurrency && !patch.researchCountry) {
      throw new ServerCommerceError("INVALID_INPUT", "At least one shopping preference is required");
    }
    const known = this.stateByCuration.get(workspaceId);
    const current = known?.preferences ?? await this.api.request<ServerUserPreferencesResult>(
      "/api/v1/me/preferences",
    );
    const updated = await this.api.request<ServerUserPreferencesResult>("/api/v1/me/preferences", {
      method: "PATCH",
      body: {
        schemaVersion: "vitlane.user-preferences.v1",
        expectedVersion: current.preferences.version,
        ...patch,
      },
    });
    this.preferencesCache = {
      value: updated,
      readAtMilliseconds: this.now(),
    };
    this.receiptByCuration.set(workspaceId, [localized(
      "다음 쇼핑부터 사용할 기본 설정을 저장했어요.",
      "The defaults for future shopping tasks were saved.",
    )]);
    return this.getWorkspace(workspaceId);
  }

  private async refreshAcceptedMutation(
    workspaceId: string,
    label: LocalizedText,
  ): Promise<WorkspaceView> {
    try {
      return await this.getWorkspace(workspaceId);
    } catch (cause) {
      if (cause instanceof VitlaneApiError && cause.status < 500 && cause.status !== 429) {
        throw cause;
      }
      const known = this.stateByCuration.get(workspaceId);
      if (!known) throw cause;
      const stale = this.toWorkspaceView(known);
      return {
        ...stale,
        activeQuestionId: null,
        processing: {
          status: "RUNNING",
          label: label["ko-KR"],
          detail: label["en-US"],
          shouldPoll: true,
        },
      };
    }
  }

  private async defaultManagedModelKey(): Promise<string> {
    const capability = await this.api.request<ManagedRunnerCapability>(
      "/api/v1/managed-runner/capability",
    );
    const defaultAvailable = capability.models.some(
      (model) => model.key === capability.defaultModelKey,
    );
    if (!capability.enabled || capability.serverExhausted || !defaultAvailable) {
      throw new ServerCommerceError(
        "MANAGED_RUNNER_UNAVAILABLE",
        "Managed planning is unavailable right now",
      );
    }
    return capability.defaultModelKey;
  }

  private commandId(candidate: string): string {
    if (uuidPattern.test(candidate)) return candidate.toLowerCase();
    const known = this.commandIdAliases.get(candidate);
    if (known) return known;
    const generated = this.idFactory();
    if (!uuidPattern.test(generated)) {
      throw new ServerCommerceError("INVALID_INPUT", "idFactory must return a UUID");
    }
    this.commandIdAliases.set(candidate, generated);
    return generated;
  }

  private async loadAndRemember(curationId: string): Promise<LoadedState> {
    const state = await this.loadState(curationId);
    this.stateByCuration.set(curationId, state);
    return state;
  }

  private async loadState(curationId: string): Promise<LoadedState> {
    const base = `/api/v1/curations/${encodeURIComponent(curationId)}`;
    const [workspace, budget, threadList, backgroundResearch, preferences] = await Promise.all([
      this.api.request<ServerWorkspace>(`${base}/workspace`),
      this.api.request<ServerBudget>(`${base}/budget`),
      this.api.request<ServerThreadList>(`${base}/threads`),
      this.readOptionalBackgroundResearch(curationId),
      this.readOptionalPreferences(),
    ]);
    const candidateFingerprint = catalogCandidateFingerprint(workspace);
    const cached = this.stateByCuration.get(curationId);
    const cacheLifetime = cached?.hydrationIncomplete ? 10_000 : 60_000;
    const canReuseHydration = cached?.candidateFingerprint === candidateFingerprint
      && this.now() - cached.hydratedAtMilliseconds < cacheLifetime;
    const hydrated = canReuseHydration && cached
      ? { candidates: cached.candidates, incomplete: cached.hydrationIncomplete }
      : await this.hydrateVisibleCandidates(curationId, workspace);
    return {
      workspace,
      budget,
      threads: threadList.threads,
      candidates: hydrated.candidates,
      hydrationIncomplete: hydrated.incomplete,
      candidateFingerprint,
      hydratedAtMilliseconds: canReuseHydration && cached
        ? cached.hydratedAtMilliseconds
        : this.now(),
      backgroundResearch,
      preferences,
    };
  }

  private async readOptionalBackgroundResearch(
    curationId: string,
  ): Promise<ServerBackgroundResearch | null> {
    try {
      return await this.api.request<ServerBackgroundResearch>(
        `/api/v1/curations/${encodeURIComponent(curationId)}/background-research`,
      );
    } catch (cause) {
      if (isOptionalCapabilityUnavailable(cause)) return null;
      throw cause;
    }
  }

  private async readOptionalPreferences(): Promise<ServerUserPreferencesResult | null> {
    const cached = this.preferencesCache;
    if (cached && this.now() - cached.readAtMilliseconds < 60_000) return cached.value;
    try {
      const value = await this.api.request<ServerUserPreferencesResult>("/api/v1/me/preferences");
      this.preferencesCache = { value, readAtMilliseconds: this.now() };
      return value;
    } catch (cause) {
      if (isOptionalCapabilityUnavailable(cause)) {
        this.preferencesCache = { value: null, readAtMilliseconds: this.now() };
        return null;
      }
      throw cause;
    }
  }

  private async hydrateVisibleCandidates(
    curationId: string,
    workspace: ServerWorkspace,
  ): Promise<{ candidates: ServerHydratedCandidate[]; incomplete: boolean }> {
    const requests: Array<Promise<ServerHydration>> = [];
    const candidates = new Map<string, ServerHydratedCandidate>();
    for (const pool of workspace.catalogResearch.pools) {
      const targetTitle = workspace.targets.find((target) => target.id === pool.targetId)?.title;
      const bySource = new Map<string, ServerCatalogCandidate[]>();
      for (const candidate of pool.products) {
        const source = candidate.source || "SHOPIFY";
        candidates.set(candidate.candidateId, {
          ...candidate,
          source,
          ...(candidate.intentPoint || targetTitle ? {
            title: candidate.intentPoint || targetTitle,
          } : {}),
          hydration: {
            status: "UNRESOLVED",
            reasonCode: "CATALOG_RESEARCH_HYDRATION_PENDING",
            retryable: true,
          },
        });
        const current = bySource.get(source) ?? [];
        current.push(candidate);
        bySource.set(source, current);
      }
      for (const [source, sourceCandidates] of bySource) {
        // Shopify is the only provider with a target-scoped hydration contract.
        // Amazon and every Korean external mall require one CANDIDATE request
        // with the canonical candidate ID so the server can enforce product
        // identity and provider-call accounting.
        if (source !== "SHOPIFY") {
          for (const candidate of sourceCandidates) {
            requests.push(this.api.request<ServerHydration>(
              `/api/v1/curations/${encodeURIComponent(curationId)}/catalog-research/hydrations`,
              {
                method: "POST",
                body: {
                  scope: "CANDIDATE",
                  source,
                  targetId: pool.targetId,
                  candidateId: candidate.candidateId,
                },
              },
            ));
          }
        } else {
          requests.push(this.api.request<ServerHydration>(
            `/api/v1/curations/${encodeURIComponent(curationId)}/catalog-research/hydrations`,
            {
              method: "POST",
              body: { scope: "VISIBLE_TARGET", source, targetId: pool.targetId },
            },
          ));
        }
      }
    }
    if (requests.length === 0) return { candidates: [...candidates.values()], incomplete: false };
    const settled = await Promise.allSettled(requests);
    let incomplete = false;
    for (const result of settled) {
      if (result.status === "rejected") {
        if (result.reason instanceof VitlaneApiError) {
          // Authentication and non-retryable client errors mean the mobile
          // request does not match the canonical server contract. Surfacing
          // them prevents a broken adapter from masquerading as partial data.
          if (result.reason.status < 500 && result.reason.status !== 429) {
            throw result.reason;
          }
        }
        incomplete = true;
        continue;
      }
      for (const pool of result.value.pools) {
        for (const candidate of pool.products ?? []) {
          candidates.set(candidate.candidateId, candidate);
          if (candidate.hydration?.status !== "READY") incomplete = true;
        }
      }
    }
    if ([...candidates.values()].some((candidate) => candidate.hydration?.status !== "READY")) {
      incomplete = true;
    }
    return { candidates: [...candidates.values()], incomplete };
  }

  private toWorkspaceView(state: LoadedState): WorkspaceView {
    const { workspace, budget, threads } = state;
    const commandReceipt = this.receiptByCuration.get(workspace.curation.id);
    this.receiptByCuration.delete(workspace.curation.id);
    const questionProjection = projectQuestions(threads, workspace.curation.version);
    const candidates = state.candidates.map(candidateView);
    const cartCandidateIds = new Set(
      workspace.cart.selections.map((selection) => selection.selection.candidateId),
    );
    const selectedCandidateIds = candidates
      .filter((candidate) => cartCandidateIds.has(candidate.id))
      .map((candidate) => candidate.id);
    const sourceCoverage = aggregateCoverage(
      workspace.catalogResearch.pools.flatMap((pool) => pool.sourceCoverage ?? []),
    );
    const processing = processingView(
      threads,
      workspace.activeWork,
      workspace.intelligence ?? [],
    );
    const agentMessages = projectAgentMessages(workspace);
    const activity = projectWorkspaceActivity(workspace, processing, agentMessages);
    const budgetConfirmed = currentBudgetWasExplicitlySet(
      threads,
      workspace.plan,
      budget,
    );
    const conditions: ConditionChipView[] = [
      {
        id: "location",
        label: localized("조사 지역", "Research area"),
        value: localized(
          [workspace.plan.locationContext.city, workspace.plan.locationContext.country].filter(Boolean).join(", "),
          [workspace.plan.locationContext.city, workspace.plan.locationContext.country].filter(Boolean).join(", "),
        ),
        confirmed: false,
        reason: localized(
          "앱에 설정된 조사 지역을 사용했어요.",
          "This uses the research region configured in the app.",
        ),
      },
      {
        id: "budget",
        label: localized("예산", "Budget"),
        value: budget.enabled && budget.totalAmount !== null
          ? localized(formatAmount(serverMoney(budget.currency, budget.totalAmount)), formatAmount(serverMoney(budget.currency, budget.totalAmount)))
          : localized("제한 없음", "No limit"),
        confirmed: budgetConfirmed,
        ...(
          budgetConfirmed
            ? {}
            : { reason: localized(
                "요청을 바탕으로 서버가 정한 현재 예산이에요.",
                "This is the current budget inferred by the server from the request.",
              ) }
        ),
      },
      ...workspace.targets.filter((target) => !target.removedAt).map((target) => ({
        id: `target:${target.id}`,
        label: localized("찾는 항목", "Target"),
        value: localized(target.title, target.title),
        confirmed: Boolean(target.confirmedAt),
        ...(!target.confirmedAt ? { reason: localized(
          "서버가 요청에서 해석한 항목이에요.",
          "This target was inferred by the server from the request.",
        ) } : {}),
      })),
    ];
    const providerIncomplete = state.hydrationIncomplete || sourceCoverage.some(
      (source) => ["PARTIAL", "FAILED", "UNSUPPORTED", "SKIPPED"].includes(source.status),
    );
    const liveNotice = providerIncomplete
      ? localized(
          "일부 소스는 불러오지 못했어요. 표시된 결과는 실제 서버 응답이에요.",
          "Some sources could not be loaded. The displayed results are from the live server.",
        )
      : localized(
          "실제 서버에서 확인한 최신 결과예요.",
          "These are current results returned by the live server.",
        );
    return {
      id: workspace.curation.id,
      mode: "server",
      title: localized(
        workspace.targets.length === 1 ? workspace.targets[0]?.title ?? "쇼핑 준비" : "쇼핑 준비",
        workspace.targets.length === 1 ? workspace.targets[0]?.title ?? "Shopping workspace" : "Shopping workspace",
      ),
      intent: workspace.plan.originalIntent,
      revision: workspace.curation.version,
      notice: liveNotice,
      noticeTone: sourceCoverage.some((source) => source.status === "FAILED")
        ? "danger"
        : providerIncomplete
          ? "warning"
          : "positive",
      sourceLabel: localized("Vitlane 실시간 카탈로그", "Vitlane live catalog"),
      conditions,
      questions: questionProjection.questions,
      answers: questionProjection.answers,
      activeQuestionId: questionProjection.activeQuestionId,
      plan: this.planView(workspace, budget),
      budgetOptions: budgetOptions(budget),
      followUpSuggestions: [],
      candidates,
      activeCandidateIds: candidates.map((candidate) => candidate.id),
      selectedCandidateIds,
      lastChange: commandReceipt ?? threadReceipt(threads, workspace),
      goal: projectShoppingGoal(workspace, candidates.length, selectedCandidateIds.length, questionProjection.questions, processing),
      activity,
      agentMessages,
      backgroundResearch: projectBackgroundResearch(
        state.backgroundResearch,
        workspace.targets,
      ),
      shoppingPreferences: projectShoppingPreferences(
        state.preferences,
        this.country,
        this.currency,
      ),
      references: { planId: workspace.plan.id, curationId: workspace.curation.id },
      sourceCoverage,
      ...(workspace.activeWork ? {
        activeWork: {
          status: workspace.activeWork.status,
          label: workspace.activeWork.label,
          ...(workspace.activeWork.detail ? { detail: workspace.activeWork.detail } : {}),
        },
      } : {}),
      ...(processing ? { processing } : {}),
    };
  }

  private planView(workspace: ServerWorkspace, budget: ServerBudget): PlanView {
    const items: PlanItemView[] = workspace.cart.selections.map((row) => ({
      id: row.selection.candidateId,
      kind: "product",
      title: localized(row.candidate.name, row.candidate.name),
      merchantId: null,
      merchantLabel: null,
      quantity: row.selection.quantity,
      unitPrice: serverMoney(row.unitPrice.currency, row.unitPrice.amount),
      necessity: "optional",
      state: "selected",
    }));
    return {
      id: workspace.plan.id,
      revision: workspace.curation.version,
      items,
      shippingByMerchant: {},
      totals: this.planTotals(workspace, budget),
    };
  }

  private planTotals(workspace: ServerWorkspace, budget: ServerBudget): PlanTotals {
    const cartTotal = serverMoney(workspace.cart.total.currency, workspace.cart.total.amount);
    const budgetAmount = budget.enabled && budget.totalAmount !== null
      ? parseMajorAmount(budget.totalAmount, budget.currency)
      : 0;
    const budgetMoney = money(budget.currency, budgetAmount);
    const totalComplete = workspace.cart.warnings.length === 0;
    const sameCurrency = cartTotal.currency === budget.currency;
    const comparisonState: PlanTotals["budgetComparisonState"] = !totalComplete
      ? "INCOMPLETE"
      : sameCurrency
        ? "AVAILABLE"
        : "CURRENCY_MISMATCH";
    const comparisonAvailable = comparisonState === "AVAILABLE";
    return {
      subtotal: cartTotal,
      knownShipping: money(cartTotal.currency, 0),
      knownTotal: cartTotal,
      totalState: totalComplete ? "COMPLETE" : "PARTIAL",
      hasUnknownFees: !totalComplete,
      unknownMerchantIds: [],
      budget: budgetMoney,
      budgetState: budget.enabled && budget.totalAmount !== null ? "LIMITED" : "UNLIMITED",
      remainingBudget: money(
        budget.currency,
        comparisonAvailable ? Math.max(0, budgetAmount - cartTotal.amount) : 0,
      ),
      overBudget: money(
        budget.currency,
        comparisonAvailable ? Math.max(0, cartTotal.amount - budgetAmount) : 0,
      ),
      budgetComparisonState: comparisonState,
    };
  }
}

function projectShoppingGoal(
  workspace: ServerWorkspace,
  candidateCount: number,
  selectedCount: number,
  questions: readonly QuestionView[],
  processing: WorkspaceView["processing"],
): ShoppingGoalView {
  const activeTargets = workspace.targets.filter((target) => !target.removedAt);
  const status: ShoppingGoalView["status"] = processing?.status === "FAILED"
    ? "failed"
    : processing?.status === "RESULT_CONFIRMATION_REQUIRED"
      ? "review_required"
      : questions.some((question) => question.state === "unanswered")
        ? "needs_input"
        : processing?.shouldPoll
          ? "working"
          : "ready";
  const title = activeTargets.length === 1
    ? localized(activeTargets[0]?.title ?? "쇼핑 목표", activeTargets[0]?.title ?? "Shopping goal")
    : localized("쇼핑 목표", "Shopping goal");
  return {
    title,
    status,
    activeTargetCount: activeTargets.length,
    candidateCount,
    selectedCount,
    unresolvedQuestionCount: questions.filter((question) => question.state === "unanswered").length,
    coverage: workspace.coverage,
  };
}

function projectAgentMessages(workspace: ServerWorkspace): AgentMessageView[] {
  const messages = workspace.conversation?.messages ?? [];
  const failedResponseIds = new Set(messages.flatMap((message) => (
    message.kind === "ERROR" && message.responseId ? [message.responseId] : []
  )));
  return messages.filter((message) => !(
    message.kind === "RESULT"
    && message.status === "SUPERSEDED"
    && message.content.code?.trim() === "RESEARCH_PARTIAL_FAILURE"
    && Boolean(message.responseId && failedResponseIds.has(message.responseId))
  )).map((message) => {
    const code = message.content.code?.trim() || message.content.reasonCode?.trim() || message.kind;
    return {
      id: message.id,
      kind: message.kind,
      status: message.status,
      version: message.version,
      code,
      title: agentMessageTitle(code, message.kind),
      body: message.content.body?.trim() || message.content.targetTitle?.trim() || "",
      ...(message.content.locale ? { contentLocale: message.content.locale } : {}),
      ...(message.content.targetTitle ? { targetTitle: message.content.targetTitle } : {}),
      ...(message.content.availableAt ? { availableAt: message.content.availableAt } : {}),
      createdAt: message.createdAt ?? "",
    };
  });
}

function agentMessageTitle(
  code: string,
  kind: AgentMessageView["kind"],
): LocalizedText {
  switch (code) {
    case "SUBSCRIBE_DEALS":
      return localized("조건에 맞는 딜을 지켜볼까요?", "Watch for deals matching these conditions?");
    case "LOW_AXIS_FIT":
      return localized("중요 조건을 더 잘 맞춰볼게요", "Search again for a better fit");
    case "FEW_NEW_CANDIDATES":
      return localized("검색 범위를 넓힐 수 있어요", "The search can be broadened");
    case "SOURCES_RATE_LIMITED":
      return localized("건너뛴 판매처를 다시 확인할까요?", "Check the skipped stores again?");
    default:
      if (kind === "ERROR") return localized("확인이 필요한 문제", "An issue needs attention");
      if (kind === "NOTICE") return localized("쇼핑 업데이트", "Shopping update");
      if (kind === "CLARIFICATION") return localized("확인이 필요해요", "A detail needs confirmation");
      if (kind === "PROPOSAL") return localized("다음 쇼핑 제안", "Next shopping proposal");
      return localized("작업 결과", "Task result");
  }
}

function projectWorkspaceActivity(
  workspace: ServerWorkspace,
  processing: WorkspaceView["processing"],
  messages: readonly AgentMessageView[],
): WorkspaceActivityView[] {
  const jobsByAction = new Map(
    (workspace.intelligence ?? []).map((job) => [job.actionId, job] as const),
  );
  const timeline = workspace.timeline.flatMap((item): WorkspaceActivityView[] => {
    const job = jobsByAction.get(item.action.id);
    const requestStatus: WorkspaceActivityView["status"] = item.result
      ? "complete"
      : job
        ? activityStatusFromJob(job.status, workspace.activeWork?.status)
        : "pending";
    const entries: WorkspaceActivityView[] = [{
      id: `${item.action.id}:request`,
      kind: "request",
      label: timelineActionLabel(item.action.type),
      ...(item.displayBody ? { body: item.displayBody } : {}),
      occurredAt: item.action.createdAt,
      status: requestStatus,
    }];
    if (item.result) {
      entries.push({
        id: `${item.action.id}:result`,
        kind: "result",
        label: timelineResultLabel(item.result.kind),
        body: item.result.summary,
        occurredAt: item.result.occurredAt,
        status: "complete",
      });
    }
    return entries;
  });
  const knownActionIds = new Set(workspace.timeline.map((item) => item.action.id));
  const orphanedWork = (workspace.intelligence ?? [])
    .filter((job) => !knownActionIds.has(job.actionId))
    .map((job): WorkspaceActivityView => ({
      id: `job:${job.jobId}`,
      kind: "work",
      label: job.targetKind === "PLANNING_TASK"
        ? localized("요청 해석", "Interpreting request")
        : localized("상품 조사", "Researching products"),
      ...(job.failureCode ? { body: job.failureCode } : {}),
      status: activityStatusFromJob(job.status, workspace.activeWork?.status),
    }));
  const messageEntries = messages
    .filter((message) => message.kind !== "PROPOSAL")
    .map((message): WorkspaceActivityView => ({
      id: `message:${message.id}`,
      kind: "message",
      label: message.title,
      ...(message.body ? { body: message.body } : {}),
      ...(message.createdAt ? { occurredAt: message.createdAt } : {}),
      status: message.kind === "ERROR" || message.code === "RESEARCH_PARTIAL_FAILURE"
        ? "failed"
        : message.status === "PENDING"
          ? "pending"
          : "complete",
    }));
  if (timeline.length === 0 && orphanedWork.length === 0 && processing) {
    orphanedWork.push({
      id: `workspace:${workspace.curation.id}:${workspace.curation.version}`,
      kind: "work",
      label: localized(processing.label, processing.detail ?? processing.label),
      status: processing.status === "RESULT_CONFIRMATION_REQUIRED"
        ? "review_required"
        : processing.status === "FAILED"
          ? "failed"
          : processing.status === "CANCELLED"
            ? "cancelled"
            : processing.shouldPoll
              ? "running"
              : "complete",
    });
  }
  return [...timeline, ...orphanedWork, ...messageEntries];
}

function activityStatusFromJob(
  status: ServerWorkspace["intelligence"][number]["status"],
  activeWorkStatus?: ServerActiveWork["status"],
): WorkspaceActivityView["status"] {
  if (activeWorkStatus === "RESULT_CONFIRMATION_REQUIRED") return "review_required";
  if (status === "PENDING") return "pending";
  if (status === "RUNNING") return "running";
  if (status === "FAILED") return "failed";
  if (status === "CANCELLED") return "cancelled";
  return "complete";
}

function timelineActionLabel(type: string): LocalizedText {
  switch (type) {
    case "INTENT_NEXT_STEP":
      return localized("쇼핑 요청", "Shopping request");
    case "PLANNING_ADD_TARGETS":
    case "CURATION_ADD_TARGETS":
      return localized("찾을 항목 추가", "Add shopping targets");
    case "PLANNING_START_CURATING":
      return localized("상품 조사 시작", "Start product research");
    case "TARGET_RESEARCH_AGAIN":
      return localized("상품 다시 찾기", "Research products again");
    default:
      return localized("쇼핑 작업", "Shopping task");
  }
}

function timelineResultLabel(kind: string): LocalizedText {
  switch (kind) {
    case "INTENT_ACCEPTED":
      return localized("요청 접수", "Request accepted");
    case "TARGET_EXPANSION":
      return localized("항목 준비 완료", "Targets prepared");
    case "RESEARCH_STARTED":
      return localized("조사 시작", "Research started");
    case "TARGET_RESEARCHED":
      return localized("후보 업데이트", "Candidates updated");
    default:
      return localized("작업 결과", "Task result");
  }
}

function projectBackgroundResearch(
  background: ServerBackgroundResearch | null,
  targets: readonly ServerPlanResult["targets"][number][],
): BackgroundResearchView {
  if (!background) {
    return { availability: "disabled", subscriptions: [], findings: [] };
  }
  const targetTitles = new Map(targets.map((target) => [target.id, target.title] as const));
  return {
    availability: "available",
    subscriptions: background.subscriptions.map((subscription) => {
      const maximum = subscription.terms.maximumMinor === undefined
        ? null
        : moneyFromMinor(subscription.terms.currency, subscription.terms.maximumMinor);
      return {
        id: subscription.id,
        targetId: subscription.targetId,
        targetTitle: targetTitles.get(subscription.targetId) ?? subscription.terms.keywords[0] ?? "",
        status: subscription.status,
        country: subscription.terms.country,
        currency: subscription.terms.currency,
        ...(maximum ? { maximum } : {}),
        keywords: [...subscription.terms.keywords],
        expiresAt: subscription.terms.expiresAt,
        createdAt: subscription.createdAt,
      };
    }),
    findings: background.findings.map((finding) => {
      const observed = finding.product.priceMinor === undefined
        ? null
        : moneyFromMinor(finding.product.currency, finding.product.priceMinor);
      return {
        id: finding.id,
        subscriptionId: finding.subscriptionId,
        targetId: finding.targetId,
        status: finding.status,
        title: finding.product.title,
        productUrl: finding.product.url,
        ...(finding.product.imageUrl ? { imageUrl: finding.product.imageUrl } : {}),
        price: observed
          ? { kind: "OBSERVED" as const, amount: observed }
          : { kind: "UNKNOWN" as const, reasonCode: "DEAL_PRICE_UNOBSERVED" },
        provider: finding.product.provider,
        reason: finding.reason,
        observedAt: finding.product.observedAt,
        createdAt: finding.createdAt,
        ...(finding.candidateId ? { candidateId: finding.candidateId } : {}),
      };
    }),
  };
}

function projectShoppingPreferences(
  preferences: ServerUserPreferencesResult | null,
  fallbackCountry: "KR" | "US",
  fallbackCurrency: Currency,
): ShoppingPreferencesView {
  if (!preferences) {
    return {
      availability: "disabled",
      version: 0,
      effective: {
        uiLocale: fallbackCountry === "KR" ? "ko-KR" : "en-US",
        preferredCurrency: fallbackCurrency,
        researchCountry: fallbackCountry,
      },
      explicit: {},
    };
  }
  return {
    availability: "available",
    version: preferences.preferences.version,
    effective: {
      uiLocale: preferences.effective.uiLocale,
      preferredCurrency: preferences.effective.preferredCurrency,
      researchCountry: preferences.effective.researchCountry,
    },
    explicit: {
      ...(preferences.preferences.uiLocale ? { uiLocale: preferences.preferences.uiLocale } : {}),
      ...(preferences.preferences.preferredCurrency
        ? { preferredCurrency: preferences.preferences.preferredCurrency }
        : {}),
      ...(preferences.preferences.researchCountry
        ? { researchCountry: preferences.preferences.researchCountry }
        : {}),
    },
  };
}

function isOptionalCapabilityUnavailable(cause: unknown): boolean {
  if (cause instanceof VitlaneApiError) {
    if (cause.status === 401 || cause.status === 403 || cause.status === 400) return false;
    return cause.status === 404 || cause.status === 409 || cause.status === 429 || cause.status >= 500;
  }
  return cause instanceof Error;
}

function catalogCandidateFingerprint(workspace: ServerWorkspace): string {
  return workspace.catalogResearch.pools
    .map((pool) => ({
      targetId: pool.targetId,
      version: pool.version,
      candidates: pool.products
        .map((candidate) => `${candidate.source ?? "SHOPIFY"}:${candidate.candidateId}`)
        .sort(),
    }))
    .sort((left, right) => left.targetId.localeCompare(right.targetId))
    .map((pool) => `${pool.targetId}:${pool.version}:${pool.candidates.join(",")}`)
    .join("|");
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export function secureUuidV4(): string {
  if (typeof globalThis.crypto?.randomUUID === "function") return globalThis.crypto.randomUUID();
  if (typeof globalThis.crypto?.getRandomValues !== "function") {
    throw new Error("Secure UUID generation is unavailable; provide idFactory");
  }
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, "0"));
  return `${hex.slice(0, 4).join("")}-${hex.slice(4, 6).join("")}-${hex.slice(6, 8).join("")}-${hex.slice(8, 10).join("")}-${hex.slice(10).join("")}`;
}

function localized(ko: string, en: string): LocalizedText {
  return { "ko-KR": ko, "en-US": en };
}

function money(currency: Currency, amount: number): Money {
  if (!Number.isFinite(amount) || amount < 0) {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "Money amount is invalid");
  }
  return { currency, amount };
}

function serverMoney(currency: Currency, amount: string): Money {
  return money(currency, parseMajorAmount(amount, currency));
}

function parseMajorAmount(amount: string, currency: Currency): number {
  if (!/^(0|[1-9]\d*)(?:\.\d{1,2})?$/.test(amount)) {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "Server money amount is invalid");
  }
  if (currency === "KRW" && amount.includes(".")) {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "KRW amount must be an integer");
  }
  const parsed = Number(amount);
  if (!Number.isSafeInteger(parsed) && currency === "KRW") {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "Server money amount is outside the safe range");
  }
  return parsed;
}

function amountForServer(value: Money): string {
  return value.currency === "KRW" ? Math.round(value.amount).toString() : value.amount.toFixed(2);
}

function normalizeDisplayAmount(value: number, currency: Currency): number {
  return currency === "KRW" ? Math.round(value) : Math.round(value * 100) / 100;
}

function formatAmount(value: Money): string {
  return value.currency === "KRW"
    ? `KRW ${new Intl.NumberFormat("ko-KR").format(value.amount)}`
    : `USD ${new Intl.NumberFormat("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(value.amount)}`;
}

const navigationApprovedBrowserRunStates = new Set<ServerBrowserRun["state"]>([
  "NAVIGATION_APPROVED",
  "AWAITING_PREPARATION_APPROVAL",
  "PREPARATION_APPROVED",
  "USER_CONTROL",
  "RESUME_REQUIRES_OBSERVATION",
  "PAUSED",
  "READY_FOR_USER_PAYMENT",
]);

function validateBrowserRunResponse(
  response: ServerBrowserRunResponse,
  curationId: string,
  candidateId: string,
): ServerBrowserRun {
  if (response?.schemaVersion !== "vitlane.browser-run.v1" || !response.run) {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "Browser run response is invalid");
  }
  const run = response.run;
  if (
    !run.id?.trim()
    || run.curationId !== curationId
    || run.candidateId !== candidateId
    || !Number.isSafeInteger(run.version)
    || run.version < 1
  ) {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "Browser run identity is invalid");
  }
  let product: URL;
  let origin: URL;
  try {
    product = new URL(run.productUrl);
    origin = new URL(run.merchantOrigin);
  } catch {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "Browser run URL is invalid");
  }
  const merchantHost = run.merchantHost.trim().toLocaleLowerCase("en-US");
  if (
    product.protocol !== "https:"
    || origin.protocol !== "https:"
    || product.username
    || product.password
    || origin.username
    || origin.password
    || origin.pathname !== "/"
    || origin.search
    || origin.hash
    || product.origin !== origin.origin
    || product.hostname.toLocaleLowerCase("en-US") !== merchantHost
    || origin.hostname.toLocaleLowerCase("en-US") !== merchantHost
  ) {
    throw new ServerCommerceError("INVALID_SERVER_RESPONSE", "Browser run merchant origin is invalid");
  }
  return run;
}

function browserRunHasNavigationApproval(run: ServerBrowserRun): boolean {
  return navigationApprovedBrowserRunStates.has(run.state);
}

function browserRunView(run: ServerBrowserRun): BrowserRunView {
  return {
    id: run.id,
    curationId: run.curationId,
    candidateId: run.candidateId,
    productUrl: run.productUrl,
    merchantOrigin: run.merchantOrigin,
    merchantHost: run.merchantHost,
    state: run.state,
    controlOwner: run.controlOwner,
    version: run.version,
  };
}

function candidateView(candidate: ServerHydratedCandidate): CandidateView {
  const external = candidate.externalObservation;
  const amazon = candidate.variantObservation;
  const title = external?.title?.trim()
    || candidate.title?.trim()
    || candidate.intentPoint?.trim()
    || candidateReference(candidate)
    || candidate.candidateId;
  const description = external?.description?.trim()
    || candidate.description?.trim()
    || candidate.intentPoint?.trim()
    || "";
  const reasons = [candidate.intentPoint, ...(candidate.features ?? []).slice(0, 2)]
    .filter((value): value is string => Boolean(value?.trim()));
  const imageUrl = external?.imageUrl || candidate.mediaUrl;
  const observedAt = external?.observedAt || amazon?.observedAt;
  const productUrl = external?.productUrl || amazon?.productUrl || candidate.locator?.productUrl;
  const price = candidatePrice(candidate);
  return {
    id: candidate.candidateId,
    title: localized(title, title),
    description: localized(
      description || "상품 상세 정보를 확인하고 있어요.",
      description || "Product details are being checked.",
    ),
    badge: localized(candidate.source, candidate.source),
    price,
    itemIds: [candidate.candidateId],
    tone: candidateTone(candidate.source),
    fitReasons: (reasons.length > 0 ? reasons : ["현재 요청의 후보"])
      .map((reason) => localized(reason, reason)),
    ...(imageUrl ? { imageUrl } : {}),
    ...(candidate.mediaAlt ? { imageAlt: localized(candidate.mediaAlt, candidate.mediaAlt) } : {}),
    provenance: {
      source: candidate.source,
      ...(external?.provenance.apiProvider ? { apiProvider: external.provenance.apiProvider } : {}),
      ...(external?.provenance.apiProduct ? { apiProduct: external.provenance.apiProduct } : {}),
      ...(external?.provenance.discoveryChannel ? { discoveryChannel: external.provenance.discoveryChannel } : {}),
      ...(observedAt ? { observedAt } : {}),
      ...(productUrl ? { productUrl } : {}),
      ...(candidate.purchaseRoute ? { purchaseRoute: candidate.purchaseRoute } : {}),
      ...(candidate.hydration?.status ? { hydrationStatus: candidate.hydration.status } : {}),
      ...(amazon?.availability ? { availability: amazon.availability } : {}),
    },
  };
}

function candidatePrice(candidate: ServerHydratedCandidate): CandidatePrice {
  if (candidate.hydration?.status !== "READY") {
    return {
      kind: "UNKNOWN",
      ...(candidate.hydration?.reasonCode ? { reasonCode: candidate.hydration.reasonCode } : {}),
    };
  }

  const external = candidate.externalObservation;
  if (external) {
    if (external.price.kind === "UNKNOWN") {
      return {
        kind: "UNKNOWN",
        ...(external.price.reasonCode ? { reasonCode: external.price.reasonCode } : {}),
      };
    }
    return observedMinorPrice(external.price.amountMinor, external.price.currency);
  }

  const amazon = candidate.variantObservation;
  if (amazon) {
    if (amazon.price.kind === "UNKNOWN") {
      return { kind: "UNKNOWN", reasonCode: amazon.price.reasonCode };
    }
    return observedMinorPrice(amazon.price.amountMinor, amazon.price.currency);
  }

  if (!isCurrency(candidate.currency) || candidate.priceMinimumMinor === undefined) {
    return { kind: "UNKNOWN", reasonCode: "CATALOG_PRICE_UNOBSERVED" };
  }
  const minimum = moneyFromMinor(candidate.currency, candidate.priceMinimumMinor);
  if (!minimum) return { kind: "UNKNOWN", reasonCode: "CATALOG_PRICE_INVALID" };
  if (candidate.priceMaximumMinor !== undefined && candidate.priceMaximumMinor > candidate.priceMinimumMinor) {
    const maximum = moneyFromMinor(candidate.currency, candidate.priceMaximumMinor);
    return maximum
      ? { kind: "RANGE", minimum, maximum }
      : { kind: "UNKNOWN", reasonCode: "CATALOG_PRICE_INVALID" };
  }
  if (candidate.priceMaximumMinor !== undefined && candidate.priceMaximumMinor < candidate.priceMinimumMinor) {
    return { kind: "UNKNOWN", reasonCode: "CATALOG_PRICE_RANGE_INVALID" };
  }
  return { kind: "OBSERVED", amount: minimum };
}

function observedMinorPrice(amountMinor: number, currencyValue: string): CandidatePrice {
  if (!isCurrency(currencyValue)) return { kind: "UNKNOWN", reasonCode: "CATALOG_CURRENCY_UNSUPPORTED" };
  const observed = moneyFromMinor(currencyValue, amountMinor);
  return observed
    ? { kind: "OBSERVED", amount: observed }
    : { kind: "UNKNOWN", reasonCode: "CATALOG_PRICE_INVALID" };
}

function isCurrency(value: string | undefined): value is Currency {
  return value === "KRW" || value === "USD";
}

function candidateReference(candidate: ServerHydratedCandidate): string {
  const reference = candidate.sourceProductRef;
  if (!reference) return "";
  if ("anchorAsin" in reference) return reference.anchorAsin;
  return "productId" in reference ? reference.productId : "";
}

function candidateTone(source: string): CandidateView["tone"] {
  const tones: CandidateView["tone"][] = ["sage", "sand", "mint", "lavender"];
  const hash = [...source].reduce((sum, character) => sum + character.charCodeAt(0), 0);
  return tones[hash % tones.length] ?? "mint";
}

function findQuestion(
  threads: readonly ServerCurationThread[],
  questionId: string,
): {
  thread: ServerCurationThread;
  action: ServerThreadAction;
  question: ServerThreadQuestion;
  answer?: NonNullable<ServerThreadAction["answers"]>[number];
  waiting: boolean;
} | null {
  for (const thread of threads) {
    for (const action of thread.actions ?? []) {
      const question = [
        ...(action.questions ?? []),
        ...(action.question ? [action.question] : []),
      ].find((candidate) => candidate.id === questionId);
      if (question) {
        const answer = (action.answers ?? []).find((candidate) => candidate.questionId === questionId);
        return {
          thread,
          action,
          question,
          ...(answer ? { answer } : {}),
          waiting: action.status === "WAITING_SELECTION" && action.question?.id === questionId,
        };
      }
    }
  }
  return null;
}

function serverAnswerMatches(
  answer: NonNullable<ServerThreadAction["answers"]>[number],
  optionId: string | undefined,
  text: string | undefined,
): boolean {
  return (answer.optionId ?? "") === (optionId ?? "")
    && (answer.text?.trim() ?? "") === (text ?? "");
}

function projectQuestions(
  threads: readonly ServerCurationThread[],
  curationRevision: number,
): {
  questions: QuestionView[];
  answers: CommittedQuestionAnswer[];
  activeQuestionId: string | null;
} {
  const questions = new Map<string, QuestionView>();
  const answers = new Map<string, CommittedQuestionAnswer>();
  let activeQuestionId: string | null = null;
  for (const thread of threads) {
    for (const action of thread.actions ?? []) {
      const actionQuestions = [
        ...(action.questions ?? []),
        ...(action.question ? [action.question] : []),
      ];
      for (const question of actionQuestions) {
        const answer = (action.answers ?? []).find((candidate) => candidate.questionId === question.id);
        if (answer) {
          answers.set(question.id, {
            questionId: question.id,
            selectedOptionIds: answer.optionId ? [answer.optionId] : [],
            ...(answer.text ? { customText: answer.text } : {}),
            committedAtRevision: curationRevision,
            origin: "user",
          });
        }
        questions.set(question.id, {
          id: question.id,
          revision: thread.revision,
          title: localized(question.prompt, question.prompt),
          reason: localized(
            "이 선택에 따라 다음 작업과 후보가 달라져요.",
            "This choice changes the next action and candidate set.",
          ),
          selection: "single",
          optional: false,
          allowCustom: true,
          options: (question.options ?? []).map((option) => ({
            id: option.id,
            title: localized(option.label, option.label),
            description: localized("", ""),
          })),
          state: answer ? "answered" : "unanswered",
          selectedOptionIds: answer?.optionId ? [answer.optionId] : [],
          ...(answer?.text ? { customText: answer.text } : {}),
          ...(answer ? { answerOrigin: "user" as const } : {}),
        });
        if (action.status === "WAITING_SELECTION" && action.question?.id === question.id && !activeQuestionId) {
          activeQuestionId = question.id;
        }
      }
    }
  }
  return { questions: [...questions.values()], answers: [...answers.values()], activeQuestionId };
}

function currentBudgetWasExplicitlySet(
  threads: readonly ServerCurationThread[],
  plan: ServerWorkspace["plan"],
  budget: ServerBudget,
): boolean {
  for (const thread of threads) {
    const actions = [...(thread.actions ?? [])].reverse();
    for (const action of actions) {
      if (action.status !== "SUCCEEDED") continue;
      const effect = (action.effects ?? []).find((candidate) => candidate.kind === "BUDGET_CHANGED");
      if (!effect) continue;
      return thread.origin === "MANUAL" && budgetEffectMatches(effect.after, budget);
    }
  }
  const request = plan.budgetRequest;
  if (request?.inputMode !== "EXPLICIT") return false;
  if (!budget.enabled) return request.totalAmount === null;
  return request.totalAmount !== null
    && request.currency === budget.currency
    && sameServerAmount(request.totalAmount, budget.totalAmount);
}

function budgetEffectMatches(
  after: {
    enabled?: boolean;
    currency?: ServerBudget["currency"];
    totalAmount?: string | null;
  } | undefined,
  budget: ServerBudget,
): boolean {
  if (!after || after.enabled !== budget.enabled) return false;
  if (!budget.enabled) return after.totalAmount === null;
  return after.currency === budget.currency
    && after.totalAmount !== null
    && after.totalAmount !== undefined
    && sameServerAmount(after.totalAmount, budget.totalAmount);
}

function sameServerAmount(left: string | null, right: string | null): boolean {
  if (left === null || right === null) return left === right;
  const leftNumber = Number(left);
  const rightNumber = Number(right);
  return Number.isFinite(leftNumber) && Number.isFinite(rightNumber) && leftNumber === rightNumber;
}

function budgetOptions(budget: ServerBudget): Money[] {
  const current = budget.enabled && budget.totalAmount !== null
    ? parseMajorAmount(budget.totalAmount, budget.currency)
    : 0;
  const unit = budget.currency === "KRW" ? 10_000 : 10;
  const values = current > 0
    ? [current, Math.max(unit, Math.round((current * 0.8) / unit) * unit), Math.round((current * 1.2) / unit) * unit]
    : budget.currency === "KRW" ? [50_000, 100_000, 150_000] : [50, 100, 150];
  return [...new Set(values)].sort((left, right) => left - right)
    .map((amount) => money(budget.currency, amount));
}

function aggregateCoverage(coverage: readonly ServerSourceCoverage[]): ServerSourceCoverage[] {
  const severity: Record<ServerSourceCoverage["status"], number> = {
    EMPTY: 1,
    SUCCEEDED: 2,
    SKIPPED: 3,
    UNSUPPORTED: 4,
    PARTIAL: 5,
    FAILED: 6,
  };
  const keyed = new Map<string, ServerSourceCoverage>();
  for (const item of coverage) {
    const previous = keyed.get(item.source);
    if (!previous) {
      keyed.set(item.source, { ...item });
      continue;
    }
    const worst = severity[item.status] > severity[previous.status] ? item : previous;
    const emptyAndSucceeded = (item.status === "EMPTY" && previous.status === "SUCCEEDED")
      || (item.status === "SUCCEEDED" && previous.status === "EMPTY");
    keyed.set(item.source, {
      source: item.source,
      status: emptyAndSucceeded ? "PARTIAL" : worst.status,
      candidateCount: previous.candidateCount + item.candidateCount,
      ...(worst.reasonCode ? { reasonCode: worst.reasonCode } : {}),
    });
  }
  return [...keyed.values()];
}

function threadReceipt(
  threads: readonly ServerCurationThread[],
  workspace: ServerWorkspace,
): readonly LocalizedText[] {
  const latest = threads[0];
  if (latest) {
    const response = (latest.actions ?? []).find((action) => action.response)?.response?.body;
    if (response) return [localized(response, response)];
    if (["INTERPRETING", "RUNNING"].includes(latest.status)) {
      return [localized("요청을 처리하고 있어요.", "Your request is being processed.")];
    }
    if (latest.status === "FAILED") {
      return [localized("요청을 완료하지 못했어요.", "The request could not be completed.")];
    }
  }
  const summary = [...workspace.timeline].reverse().find((item) => item.result?.summary)?.result?.summary;
  return summary ? [localized(summary, summary)] : [];
}

function processingView(
  threads: readonly ServerCurationThread[],
  activeWork?: ServerWorkspace["activeWork"],
  intelligence: readonly ServerWorkspace["intelligence"][number][] = [],
): WorkspaceView["processing"] | undefined {
  const latest = threads[0];
  if (latest && latest.status !== "SUCCEEDED") {
    return {
      status: latest.status,
      label: latest.request,
      ...(latest.reasonCode ? { detail: latest.reasonCode } : {}),
      shouldPoll: latest.status === "INTERPRETING" || latest.status === "RUNNING",
    };
  }
  if (activeWork) {
    return {
      status: activeWork.status === "RESULT_CONFIRMATION_REQUIRED"
        ? "RESULT_CONFIRMATION_REQUIRED"
        : "RUNNING",
      label: activeWork.label,
      ...(activeWork.detail ? { detail: activeWork.detail } : {}),
      shouldPoll: activeWork.status === "QUEUED" || activeWork.status === "RUNNING",
    };
  }
  const newestJob = intelligence.at(-1);
  const newestCohort = newestJob
    ? intelligence.filter((job) => job.actionId === newestJob.actionId)
    : [];
  const activeJob = newestCohort.find((job) => job.status === "PENDING" || job.status === "RUNNING");
  const failedJob = newestCohort.find((job) => job.status === "FAILED");
  const cancelledJob = newestCohort.find((job) => job.status === "CANCELLED");
  const projectedJob = activeJob ?? failedJob ?? cancelledJob ?? newestJob;
  if (projectedJob) {
    const planning = projectedJob.targetKind === "PLANNING_TASK";
    const label = planning ? "Target 계획" : "후보 조사";
    const detail = projectedJob.failureCode || projectedJob.queueReason;
    return {
      status: projectedJob.status === "PENDING" ? "RUNNING" : projectedJob.status,
      label,
      ...(detail ? { detail } : {}),
      shouldPoll: projectedJob.status === "PENDING" || projectedJob.status === "RUNNING",
    };
  }
  return latest ? {
    status: latest.status,
    label: latest.request,
    ...(latest.reasonCode ? { detail: latest.reasonCode } : {}),
    shouldPoll: false,
  } : undefined;
}
