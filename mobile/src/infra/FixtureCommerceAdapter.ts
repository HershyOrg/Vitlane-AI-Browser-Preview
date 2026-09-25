import fixtureJson from "../fixtures/scenarios.json";
import type { CommerceGateway } from "../commerce";
import type {
  AgentMessageResponse,
  AgentMessageStatus,
  BackgroundResearchView,
  BrowserRunStartContext,
  BrowserRunView,
  BudgetChangePreview,
  CandidateView,
  ChoiceOptionView,
  CommandContext,
  CommittedQuestionAnswer,
  ConditionChipView,
  LocalizedText,
  PlanItemState,
  PlanItemView,
  PlanView,
  QuestionAnswerInput,
  QuestionView,
  ServiceSlot,
  ShoppingPreferencesPatch,
  ShoppingPreferencesView,
  WorkspaceView,
} from "../domain/models";
import { FixtureDomainError } from "../domain/models";
import { calculatePlanTotals } from "../domain/totals";

interface FixtureItem {
  id: string;
  kind: "product" | "service";
  title: LocalizedText;
  merchantId: string | null;
  merchantLabel: LocalizedText | null;
  quantity: number;
  unitPrice: number;
  necessity: "required" | "optional";
  state: PlanItemState;
  slot?: ServiceSlot;
  partySize?: number;
  locationLabel?: LocalizedText;
  cancellationSummary?: LocalizedText;
}

interface FixtureQuestion {
  id: string;
  title: LocalizedText;
  reason: LocalizedText;
  selection: "single";
  optional: boolean;
  allowCustom: boolean;
  options: ChoiceOptionView[];
  assumedOptionIds: string[];
}

interface FixtureData {
  schemaVersion: 1;
  mode: "fixture";
  observedAt: string;
  notice: LocalizedText;
  sourceLabel: LocalizedText;
  workspace: {
    id: string;
    title: LocalizedText;
    intent: LocalizedText;
    conditions: ConditionChipView[];
    basePlan: {
      id: string;
      revision: number;
      budget: number;
      items: FixtureItem[];
      shippingByMerchant: Record<string, number | null>;
    };
    budgetPlan: {
      budget: number;
      items: Array<{ id: string; unitPrice: number; state: PlanItemState }>;
    };
  };
  questions: FixtureQuestion[];
  candidates: Array<Omit<CandidateView, "price" | "provenance"> & { total: number }>;
  followUps: Array<{
    id: string;
    action: "change_slot_1700" | "remove_decor";
    phrases: string[];
    summary: LocalizedText;
  }>;
}

interface StoredWorkspace {
  id: string;
  title: LocalizedText;
  intent: string;
  revision: number;
  conditions: ConditionChipView[];
  answers: CommittedQuestionAnswer[];
  activeQuestionId: string | null;
  plan: PlanView;
  activeCandidateIds: string[];
  selectedCandidateId: string;
  lastChange: LocalizedText[];
}

interface IdempotencyReceipt {
  operation: string;
  fingerprint: string;
  result: unknown;
}

const fixture = fixtureJson as unknown as FixtureData;

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T;

const won = (amount: number) => ({ currency: "KRW" as const, amount });

const localized = (ko: string, en: string): LocalizedText => ({
  "ko-KR": ko,
  "en-US": en,
});

const fixtureItemToView = (item: FixtureItem): PlanItemView => ({
  ...clone(item),
  unitPrice: won(item.unitPrice),
});

const candidateToView = (
  candidate: FixtureData["candidates"][number],
): CandidateView => {
  const { total, ...view } = clone(candidate);
  return {
    ...view,
    price: { kind: "OBSERVED", amount: won(total) },
    provenance: { source: "FIXTURE" },
  };
};

const fixtureFindingCandidate = (): CandidateView => ({
  id: "fixture-imported-candidate",
  title: localized("6인용 홈파티 간식 묶음", "Party snack bundle for six"),
  description: localized("조건 기반 확인에서 발견한 비교 후보", "A comparison candidate found by condition monitoring"),
  badge: localized("새 발견", "New find"),
  price: { kind: "OBSERVED", amount: won(29_900) },
  itemIds: ["snacks"],
  tone: "mint",
  fitReasons: [localized(
    "저장한 인원과 가격 상한에 맞아요.",
    "Matches the saved party size and price ceiling.",
  )],
  provenance: {
    source: "FIXTURE",
    productUrl: "https://example.com/vitlane-review-deal",
    purchaseRoute: "EXTERNAL",
    availability: "AVAILABLE",
    observedAt: "2026-09-24T09:00:00+09:00",
  },
});

const questionById = (questionId: string): FixtureQuestion => {
  const question = fixture.questions.find((candidate) => candidate.id === questionId);
  if (!question) {
    throw new FixtureDomainError("NOT_FOUND", `Fixture question not found: ${questionId}`);
  }
  return question;
};

const slotAt = (hour: 16 | 17): ServiceSlot => ({
  id: `demo-c-slot-${hour}00`,
  start: `2026-10-03T${hour}:00:00+09:00`,
  end: `2026-10-03T${hour}:30:00+09:00`,
  timeZone: "Asia/Seoul",
  state: "known",
});

const planWith = (
  current: PlanView,
  revision: number,
  items: readonly PlanItemView[],
  budgetAmount = current.totals.budget.amount,
): PlanView => ({
  id: current.id,
  revision,
  items: clone(items),
  shippingByMerchant: clone(current.shippingByMerchant),
  totals: calculatePlanTotals(items, current.shippingByMerchant, budgetAmount),
});

/**
 * Deterministic, in-memory commerce boundary for the intent-to-workspace review flow.
 *
 * This adapter never calls a network, payment, booking, or reservation API. It
 * stores only committed answers; an in-progress question draft belongs to UI
 * state and must be discarded or restored by the sheet component.
 */
export class FixtureCommerceAdapter implements CommerceGateway {
  public readonly mode = "fixture" as const;

  private workspace: StoredWorkspace | null = null;
  private readonly previews = new Map<string, BudgetChangePreview>();
  private readonly receipts = new Map<string, IdempotencyReceipt>();
  private proposalStatus: AgentMessageStatus = "PENDING";
  private subscriptionStatus: BackgroundResearchView["subscriptions"][number]["status"] = "ACTIVE";
  private findingStatus: BackgroundResearchView["findings"][number]["status"] = "NEW";
  private preferences: ShoppingPreferencesView = {
    availability: "available",
    version: 0,
    effective: {
      uiLocale: "ko-KR",
      preferredCurrency: "KRW",
      researchCountry: "KR",
    },
    explicit: {},
  };

  public async createWorkspace(
    intent: string,
    context: CommandContext,
  ): Promise<WorkspaceView | null> {
    const normalizedIntent = intent.trim();
    if (normalizedIntent.length === 0) {
      return null;
    }

    return this.mutateOnce(
      "createWorkspace",
      context,
      { intent: normalizedIntent },
      () => {
        this.assertRevision(context.baseRevision, 0);
        if (this.workspace) {
          throw new FixtureDomainError(
            "UNSUPPORTED_FIXTURE_CHANGE",
            "This focused fixture adapter supports one workspace per instance",
          );
        }

        const base = fixture.workspace.basePlan;
        const items = base.items.map(fixtureItemToView);
        const plan: PlanView = {
          id: base.id,
          revision: base.revision,
          items,
          shippingByMerchant: clone(base.shippingByMerchant),
          totals: calculatePlanTotals(items, base.shippingByMerchant, base.budget),
        };
        this.workspace = {
          id: fixture.workspace.id,
          title: clone(fixture.workspace.title),
          intent: normalizedIntent,
          revision: base.revision,
          conditions: clone(fixture.workspace.conditions),
          answers: fixture.questions.map((question) => ({
            questionId: question.id,
            selectedOptionIds: [...question.assumedOptionIds],
            committedAtRevision: base.revision,
            origin: "assumed" as const,
          })),
          activeQuestionId: null,
          plan,
          activeCandidateIds: ["party-balanced", "party-goods-only", "party-budget"],
          selectedCandidateId: "party-balanced",
          lastChange: [],
        };
        return this.toView(this.workspace);
      },
    );
  }

  public async getWorkspace(workspaceId: string): Promise<WorkspaceView> {
    return this.toView(this.requireWorkspace(workspaceId));
  }

  public async getPlan(planId: string): Promise<PlanView> {
    const state = this.requireAnyWorkspace();
    if (state.plan.id !== planId) {
      throw new FixtureDomainError("NOT_FOUND", `Fixture plan not found: ${planId}`);
    }
    return clone(state.plan);
  }

  public async getCandidates(workspaceId: string): Promise<readonly CandidateView[]> {
    const state = this.requireWorkspace(workspaceId);
    const active = new Set(this.viewActiveCandidateIds(state));
    return this.allCandidateViews()
      .filter((candidate) => active.has(candidate.id));
  }

  public async startBrowserRun(
    workspaceId: string,
    candidateId: string,
    _context: BrowserRunStartContext,
  ): Promise<BrowserRunView> {
    const state = this.requireWorkspace(workspaceId);
    const active = new Set(this.viewActiveCandidateIds(state));
    const candidate = this.allCandidateViews().find((item) => item.id === candidateId);
    if (!candidate || !active.has(candidate.id) || !candidate.provenance.productUrl) {
      throw new FixtureDomainError("NOT_FOUND", `Fixture browser candidate not found: ${candidateId}`);
    }
    const productUrl = new URL(candidate.provenance.productUrl);
    if (productUrl.protocol !== "https:" || !productUrl.hostname) {
      throw new FixtureDomainError("UNSUPPORTED_FIXTURE_CHANGE", "Fixture browser URL must be HTTPS");
    }
    return {
      id: `fixture-browser-${candidate.id}`,
      curationId: workspaceId,
      candidateId,
      productUrl: productUrl.toString(),
      merchantOrigin: productUrl.origin,
      merchantHost: productUrl.hostname,
      state: "NAVIGATION_APPROVED",
      controlOwner: "NONE",
      version: 2,
    };
  }

  public async answerQuestion(
    workspaceId: string,
    answer: QuestionAnswerInput,
    context: CommandContext,
  ): Promise<WorkspaceView> {
    return this.mutateOnce(
      "answerQuestion",
      context,
      { workspaceId, answer },
      () => {
        const state = this.requireWorkspace(workspaceId);
        this.assertRevision(context.baseRevision, state.revision);
        const question = questionById(answer.questionId);
        this.validateAnswer(question, answer);
        if (
          question.id === "q-slot"
          && state.plan.items.find((item) => item.id === "cake")?.state !== "selected"
        ) {
          throw new FixtureDomainError(
            "UNSUPPORTED_FIXTURE_CHANGE",
            "Pickup time cannot be changed after cake service has been removed",
          );
        }

        const nextRevision = state.revision + 1;
        const committed: CommittedQuestionAnswer = {
          questionId: question.id,
          selectedOptionIds: [...answer.selectedOptionIds],
          ...(answer.customText?.trim() ? { customText: answer.customText.trim() } : {}),
          committedAtRevision: nextRevision,
          origin: "user",
        };
        state.answers = [
          ...state.answers.filter((existing) => existing.questionId !== question.id),
          committed,
        ];

        const selectedOptionId = answer.selectedOptionIds[0];
        if (question.id === "q-region") {
          const customRegion = answer.customText?.trim();
          const region = selectedOptionId === "mapo"
            ? localized("서울 마포구", "Mapo-gu, Seoul")
            : localized(customRegion ?? "다른 지역", customRegion ?? "Another area");
          state.conditions = this.upsertCondition(state.conditions, {
            id: "region",
            label: localized("지역", "Area"),
            value: region,
            confirmed: true,
            refinementQuestionId: "q-region",
          });
          state.activeQuestionId = null;
        }

        if (question.id === "q-service") {
          if (selectedOptionId === "goods-only") {
            const items = state.plan.items.map((item) =>
              item.id === "cake" ? { ...item, state: "removed" as const } : item,
            );
            state.plan = planWith(state.plan, nextRevision, items);
            const decorRemoved = items.find((item) => item.id === "decor")?.state === "removed";
            const candidateId = state.plan.totals.budget.amount === 80000
              ? "party-goods-only-budget"
              : decorRemoved
                ? "party-goods-only-light"
                : "party-goods-only";
            state.activeCandidateIds = [candidateId];
            state.selectedCandidateId = candidateId;
            state.activeQuestionId = null;
            state.conditions = this.upsertCondition(
              state.conditions.filter((condition) => condition.id !== "pickup-time"),
              {
                id: "service",
                label: localized("케이크", "Cake"),
                value: localized("상품만 준비", "Goods only"),
                confirmed: true,
                refinementQuestionId: "q-service",
              },
            );
          } else if (answer.disposition === "no_preference") {
            state.activeQuestionId = null;
          } else {
            const items = state.plan.items.map((item) =>
              item.id === "cake" ? { ...item, state: "selected" as const } : item,
            );
            state.plan = planWith(state.plan, nextRevision, items);
            const decorRemoved = items.find((item) => item.id === "decor")?.state === "removed";
            const candidateId = state.plan.totals.budget.amount === 80000
              ? "party-budget"
              : decorRemoved
                ? "party-light"
                : "party-balanced";
            state.activeCandidateIds = candidateId === "party-balanced"
              ? ["party-balanced", "party-light", "party-budget"]
              : [candidateId];
            state.selectedCandidateId = candidateId;
            state.activeQuestionId = null;
            state.conditions = this.upsertCondition(state.conditions, {
              id: "service",
              label: localized("케이크", "Cake"),
              value: localized("픽업 포함", "Pickup included"),
              confirmed: true,
              refinementQuestionId: "q-service",
            });
            const cake = items.find((item) => item.id === "cake");
            const slotAnswer = state.answers.find((existing) => existing.questionId === "q-slot");
            const hour = cake?.slot?.start.includes("T17:") ? 17 : 16;
            state.conditions = this.upsertCondition(state.conditions, {
              id: "pickup-time",
              label: localized("픽업", "Pickup"),
              value: hour === 17
                ? localized("오후 5시", "5 PM")
                : localized("오후 4시", "4 PM"),
              confirmed: slotAnswer?.origin === "user",
              reason: localized(
                "검수용 기본 시간으로 먼저 잡았어요.",
                "This uses the review-only default time.",
              ),
              refinementQuestionId: "q-slot",
            });
          }
        }

        if (question.id === "q-slot") {
          const hour = selectedOptionId === "1700" ? 17 : 16;
          const items = state.plan.items.map((item) =>
            item.id === "cake" ? { ...item, slot: slotAt(hour) } : item,
          );
          state.plan = planWith(state.plan, nextRevision, items);
          state.activeQuestionId = null;
          state.conditions = this.upsertCondition(state.conditions, {
            id: "pickup-time",
            label: localized("픽업", "Pickup"),
            value: hour === 17
              ? localized("오후 5시", "5 PM")
              : localized("오후 4시", "4 PM"),
            confirmed: true,
            refinementQuestionId: "q-slot",
          });
        }

        state.revision = nextRevision;
        state.plan = state.plan.revision === nextRevision
          ? state.plan
          : planWith(state.plan, nextRevision, state.plan.items);
        state.lastChange = [
          localized("선택한 답변을 준비안에 반영", "Applied the selected answer to the plan"),
        ];
        return this.toView(state);
      },
    );
  }

  public async previewBudget(
    workspaceId: string,
    budgetAmount: number,
    context: CommandContext,
  ): Promise<BudgetChangePreview> {
    return this.mutateOnce(
      "previewBudget",
      context,
      { workspaceId, budgetAmount },
      () => {
        const state = this.requireWorkspace(workspaceId);
        this.assertRevision(context.baseRevision, state.revision);
        if (budgetAmount !== fixture.workspace.budgetPlan.budget) {
          throw new FixtureDomainError(
            "UNSUPPORTED_FIXTURE_CHANGE",
            "The focused fixture provides the KRW 80,000 budget revision only",
          );
        }

        const overrides = new Map(
          fixture.workspace.budgetPlan.items.map((item) => [item.id, item] as const),
        );
        const projectedItems = state.plan.items.map((item) => {
          const override = overrides.get(item.id);
          return override
            ? {
                ...item,
                unitPrice: won(override.unitPrice),
                state: item.state === "removed" ? "removed" as const : override.state,
              }
            : item;
        });
        const proposedRevision = state.revision + 1;
        const projectedPlan = planWith(
          state.plan,
          proposedRevision,
          projectedItems,
          budgetAmount,
        );
        const preview: BudgetChangePreview = {
          id: `budget-preview:${workspaceId}:${state.revision}:${budgetAmount}`,
          workspaceId,
          baseRevision: state.revision,
          proposedRevision,
          requestedBudget: won(budgetAmount),
          before: clone(state.plan.totals),
          projectedPlan,
          changes: [
            {
              itemId: "snacks",
              before: localized("간식 1개당 18,000원", "Snacks at KRW 18,000 each"),
              after: localized("간식 1개당 12,000원", "Snacks at KRW 12,000 each"),
              reason: localized("필수 수량을 유지하면서 구성을 조정", "Adjust the mix while keeping the required quantity"),
            },
            {
              itemId: "drinks",
              before: localized("음료 1개당 12,000원", "Drinks at KRW 12,000 each"),
              after: localized("음료 1개당 9,000원", "Drinks at KRW 9,000 each"),
              reason: localized("같은 수량의 더 간단한 묶음으로 변경", "Switch to a simpler bundle with the same quantity"),
            },
            {
              itemId: "decor",
              before: localized("파티 장식 포함", "Party decorations included"),
              after: localized("파티 장식 제외", "Party decorations removed"),
              reason: localized("선택 항목을 먼저 줄임", "Reduce an optional item first"),
            },
            {
              itemId: "cake",
              before: localized("케이크 픽업 42,000원", "Cake pickup at KRW 42,000"),
              after: localized("케이크 픽업 28,000원", "Cake pickup at KRW 28,000"),
              reason: localized("인원에 맞는 더 작은 구성으로 변경", "Switch to a smaller option sized for the group"),
            },
          ].filter((change) => {
            const beforeItem = state.plan.items.find((item) => item.id === change.itemId);
            const afterItem = projectedItems.find((item) => item.id === change.itemId);
            if (beforeItem?.state === "removed" && afterItem?.state === "removed") {
              return false;
            }
            return beforeItem?.state !== afterItem?.state
              || beforeItem?.unitPrice.amount !== afterItem?.unitPrice.amount;
          }),
        };
        this.previews.set(preview.id, clone(preview));
        return preview;
      },
    );
  }

  public async applyBudgetPreview(
    workspaceId: string,
    previewId: string,
    context: CommandContext,
  ): Promise<WorkspaceView> {
    return this.mutateOnce(
      "applyBudgetPreview",
      context,
      { workspaceId, previewId },
      () => {
        const state = this.requireWorkspace(workspaceId);
        this.assertRevision(context.baseRevision, state.revision);
        const preview = this.previews.get(previewId);
        if (!preview || preview.workspaceId !== workspaceId) {
          throw new FixtureDomainError("NOT_FOUND", `Fixture preview not found: ${previewId}`);
        }
        if (preview.baseRevision !== state.revision) {
          throw new FixtureDomainError(
            "REVISION_CONFLICT",
            `Preview revision ${preview.baseRevision} does not match workspace revision ${state.revision}`,
          );
        }

        const decorWasSelected = state.plan.items.find(
          (item) => item.id === "decor",
        )?.state === "selected";
        state.revision = preview.proposedRevision;
        state.plan = clone(preview.projectedPlan);
        const cakeRemoved = state.plan.items.find((item) => item.id === "cake")?.state === "removed";
        const candidateId = cakeRemoved ? "party-goods-only-budget" : "party-budget";
        state.activeCandidateIds = [candidateId];
        state.selectedCandidateId = candidateId;
        state.conditions = this.upsertCondition(state.conditions, {
          id: "budget",
          label: localized("예산", "Budget"),
          value: localized("8만 원", "KRW 80,000"),
          confirmed: true,
        });
        state.lastChange = [
          localized("예산을 8만 원으로 조정", "Adjusted the budget to KRW 80,000"),
          ...(decorWasSelected
            ? [localized("선택 항목인 파티 장식을 제외", "Removed the optional party decorations")]
            : []),
        ];
        return this.toView(state);
      },
    );
  }

  public async submitFollowUp(
    workspaceId: string,
    text: string,
    context: CommandContext,
  ): Promise<WorkspaceView> {
    const normalized = text.trim().toLocaleLowerCase("en-US");
    const matches = fixture.followUps.filter((followUp) =>
      followUp.phrases.some((phrase) => normalized.includes(phrase.toLocaleLowerCase("en-US"))),
    );
    if (matches.length === 0) {
      throw new FixtureDomainError(
        "UNSUPPORTED_FIXTURE_CHANGE",
        "The fixture follow-up supports changing pickup to 17:00 and removing decorations",
      );
    }

    return this.mutateOnce(
      "submitFollowUp",
      context,
      { workspaceId, text: normalized, actions: matches.map((match) => match.action) },
      () => {
        const state = this.requireWorkspace(workspaceId);
        this.assertRevision(context.baseRevision, state.revision);
        const cake = state.plan.items.find((item) => item.id === "cake");
        if (matches.some((match) => match.action === "change_slot_1700") && cake?.state !== "selected") {
          throw new FixtureDomainError(
            "UNSUPPORTED_FIXTURE_CHANGE",
            "Pickup time cannot be changed after cake service has been removed",
          );
        }
        const effectiveMatches = matches.filter((match) => {
          if (match.action === "change_slot_1700") {
            return cake?.slot?.start !== slotAt(17).start;
          }
          const decor = state.plan.items.find((item) => item.id === "decor");
          return decor?.state === "selected";
        });
        if (effectiveMatches.length === 0) {
          throw new FixtureDomainError(
            "UNSUPPORTED_FIXTURE_CHANGE",
            "The requested fixture follow-up would not change the current plan",
          );
        }
        const actions = new Set(effectiveMatches.map((match) => match.action));
        const nextRevision = state.revision + 1;
        const items = state.plan.items.map((item) => {
          if (item.id === "cake" && actions.has("change_slot_1700")) {
            return { ...item, slot: slotAt(17) };
          }
          if (item.id === "decor" && actions.has("remove_decor")) {
            return { ...item, state: "removed" as const };
          }
          return item;
        });

        state.revision = nextRevision;
        state.plan = planWith(state.plan, nextRevision, items);
        state.lastChange = effectiveMatches.map((match) => clone(match.summary));
        if (actions.has("change_slot_1700")) {
          state.conditions = this.upsertCondition(state.conditions, {
            id: "pickup-time",
            label: localized("픽업", "Pickup"),
            value: localized("오후 5시", "5 PM"),
            confirmed: true,
            refinementQuestionId: "q-slot",
          });
          state.answers = [
            ...state.answers.filter((answer) => answer.questionId !== "q-slot"),
            {
              questionId: "q-slot",
              selectedOptionIds: ["1700"],
              committedAtRevision: nextRevision,
              origin: "user",
            },
          ];
        }
        if (actions.has("remove_decor")) {
          const cakeRemoved = state.plan.items.find((item) => item.id === "cake")?.state === "removed";
          const candidateId = cakeRemoved
            ? state.plan.totals.budget.amount === 80000
              ? "party-goods-only-budget"
              : "party-goods-only-light"
            : state.plan.totals.budget.amount === 80000
              ? "party-budget"
              : "party-light";
          state.activeCandidateIds = [candidateId];
          state.selectedCandidateId = candidateId;
        }
        return this.toView(state);
      },
    );
  }

  public async respondToAgentMessage(
    workspaceId: string,
    messageId: string,
    messageVersion: number,
    response: AgentMessageResponse,
    context: CommandContext,
  ): Promise<WorkspaceView> {
    return this.mutateOnce(
      "respondToAgentMessage",
      context,
      { workspaceId, messageId, messageVersion, response },
      () => {
        const state = this.requireWorkspace(workspaceId);
        this.assertRevision(context.baseRevision, state.revision);
        if (messageId !== "fixture-proposal-deals" || messageVersion !== 1) {
          throw new FixtureDomainError("NOT_FOUND", "Fixture proposal not found");
        }
        const desired: AgentMessageStatus = response === "ACCEPT"
          ? "ACCEPTED"
          : response === "DISMISS"
            ? "DISMISSED"
            : "ACKNOWLEDGED";
        if (response === "ACKNOWLEDGE") {
          throw new FixtureDomainError("INVALID_ANSWER", "Fixture proposals require accept or dismiss");
        }
        if (this.proposalStatus !== "PENDING" && this.proposalStatus !== desired) {
          throw new FixtureDomainError("REVISION_CONFLICT", "Fixture proposal already has another response");
        }
        this.proposalStatus = desired;
        state.lastChange = [response === "ACCEPT"
          ? localized("딜 조건 확인 제안을 수락", "Accepted the deal monitoring proposal")
          : localized("제안을 닫음", "Closed the proposal")];
        return this.toView(state);
      },
    );
  }

  public async cancelResearchSubscription(
    workspaceId: string,
    subscriptionId: string,
  ): Promise<WorkspaceView> {
    const state = this.requireWorkspace(workspaceId);
    if (this.proposalStatus !== "ACCEPTED" || subscriptionId !== "fixture-subscription") {
      throw new FixtureDomainError("NOT_FOUND", "Fixture subscription not found");
    }
    this.subscriptionStatus = "CANCELLED";
    state.lastChange = [localized("지켜보던 조건을 중단", "Stopped watching the saved condition")];
    return this.toView(state);
  }

  public async hideResearchFinding(
    workspaceId: string,
    findingId: string,
  ): Promise<WorkspaceView> {
    const state = this.requireWorkspace(workspaceId);
    if (this.proposalStatus !== "ACCEPTED" || findingId !== "fixture-finding") {
      throw new FixtureDomainError("NOT_FOUND", "Fixture finding not found");
    }
    this.findingStatus = "HIDDEN";
    state.lastChange = [localized("발견 상품을 숨김", "Hidden the discovered product")];
    return this.toView(state);
  }

  public async importResearchFinding(
    workspaceId: string,
    findingId: string,
  ): Promise<WorkspaceView> {
    const state = this.requireWorkspace(workspaceId);
    if (this.proposalStatus !== "ACCEPTED" || findingId !== "fixture-finding") {
      throw new FixtureDomainError("NOT_FOUND", "Fixture finding not found");
    }
    this.findingStatus = "ADDED";
    state.lastChange = [localized("발견 상품을 비교 후보에 추가", "Added the discovery to comparison candidates")];
    return this.toView(state);
  }

  public async updateShoppingPreferences(
    workspaceId: string,
    patch: ShoppingPreferencesPatch,
  ): Promise<WorkspaceView> {
    const state = this.requireWorkspace(workspaceId);
    if (!patch.uiLocale && !patch.preferredCurrency && !patch.researchCountry) {
      throw new FixtureDomainError("INVALID_ANSWER", "At least one preference is required");
    }
    const effective = { ...this.preferences.effective, ...patch };
    this.preferences = {
      availability: "available",
      version: this.preferences.version + 1,
      effective,
      explicit: { ...this.preferences.explicit, ...patch },
    };
    state.lastChange = [localized("다음 쇼핑 기본 설정을 저장", "Saved defaults for future shopping tasks")];
    return this.toView(state);
  }

  private toView(state: StoredWorkspace): WorkspaceView {
    const answersByQuestion = new Map(
      state.answers.map((answer) => [answer.questionId, answer] as const),
    );
    const questions: QuestionView[] = fixture.questions.map((question) => {
      const answer = answersByQuestion.get(question.id);
      return {
        id: question.id,
        title: clone(question.title),
        reason: clone(question.reason),
        selection: question.selection,
        optional: question.optional,
        allowCustom: question.allowCustom,
        options: clone(question.options),
        revision: state.revision,
        state: answer ? "answered" : "unanswered",
        selectedOptionIds: answer ? [...answer.selectedOptionIds] : [],
        ...(answer?.customText ? { customText: answer.customText } : {}),
        ...(answer ? { answerOrigin: answer.origin } : {}),
      };
    });
    const cake = state.plan.items.find(
      (item) => item.kind === "service" && item.state === "selected",
    );
    const decor = state.plan.items.find((item) => item.id === "decor");
    const followUpSuggestions = [
      ...(cake && cake.slot?.start !== slotAt(17).start
        ? [{
            id: "slot-1700",
            text: localized("픽업을 오후 5시로", "Move pickup to 5 PM"),
          }]
        : []),
      ...(decor?.state === "selected"
        ? [{
            id: "remove-decor",
            text: localized("장식은 빼줘", "Remove decorations"),
          }]
        : []),
    ];
    const candidates = this.allCandidateViews();
    const activeCandidateIds = this.viewActiveCandidateIds(state);
    const activeCandidates = candidates.filter((candidate) => activeCandidateIds.includes(candidate.id));
    const monitoringEnabled = this.proposalStatus === "ACCEPTED";
    const backgroundResearch: BackgroundResearchView = {
      availability: "available",
      subscriptions: monitoringEnabled ? [{
        id: "fixture-subscription",
        targetId: "fixture-target-party",
        targetTitle: "홈파티 준비물",
        status: this.subscriptionStatus,
        country: "KR",
        currency: "KRW",
        maximum: won(35000),
        keywords: ["홈파티", "간식 세트"],
        expiresAt: "2026-10-24T00:00:00Z",
        createdAt: "2026-09-24T00:00:00Z",
      }] : [],
      findings: monitoringEnabled ? [{
        id: "fixture-finding",
        subscriptionId: "fixture-subscription",
        targetId: "fixture-target-party",
        status: this.findingStatus,
        title: "6인용 홈파티 간식 묶음",
        productUrl: "https://example.com/vitlane-review-deal",
        price: { kind: "OBSERVED", amount: won(29900) },
        provider: "FIXTURE",
        reason: "저장한 인원과 가격 상한에 맞는 검수용 발견 상품",
        observedAt: "2026-09-24T09:00:00+09:00",
        createdAt: "2026-09-24T09:00:00+09:00",
        ...(this.findingStatus === "ADDED" ? { candidateId: "fixture-imported-candidate" } : {}),
      }] : [],
    };

    return clone({
      id: state.id,
      mode: "fixture" as const,
      title: state.title,
      intent: state.intent,
      revision: state.revision,
      notice: fixture.notice,
      sourceLabel: fixture.sourceLabel,
      conditions: state.conditions,
      questions,
      answers: state.answers,
      activeQuestionId: state.activeQuestionId,
      plan: state.plan,
      budgetOptions: [won(fixture.workspace.budgetPlan.budget)],
      followUpSuggestions,
      candidates,
      activeCandidateIds,
      selectedCandidateIds: [state.selectedCandidateId],
      lastChange: state.lastChange,
      goal: {
        title: state.title,
        status: state.activeQuestionId ? "needs_input" : "ready",
        activeTargetCount: 4,
        candidateCount: activeCandidates.length,
        selectedCount: 1,
        unresolvedQuestionCount: state.activeQuestionId ? 1 : 0,
        coverage: "COMPLETE",
      },
      activity: [
        {
          id: `fixture-request:${state.revision}`,
          kind: "request",
          label: localized("쇼핑 요청", "Shopping request"),
          body: state.intent,
          occurredAt: "2026-09-24T08:55:00+09:00",
          status: "complete",
        },
        {
          id: `fixture-result:${state.revision}`,
          kind: "result",
          label: localized("후보 업데이트", "Candidates updated"),
          body: "조건과 예산에 맞는 준비안을 만들었어요.",
          occurredAt: "2026-09-24T09:00:00+09:00",
          status: "complete",
        },
      ],
      agentMessages: [{
        id: "fixture-proposal-deals",
        kind: "PROPOSAL",
        status: this.proposalStatus,
        version: 1,
        code: "SUBSCRIBE_DEALS",
        title: localized("비슷한 딜을 계속 지켜볼까요?", "Keep watching for similar deals?"),
        body: "같은 조건에서 3만 5천 원 이하의 새 상품이 나오면 발견 목록에 모아둘게요.",
        contentLocale: "ko-KR",
        targetTitle: "홈파티 준비물",
        createdAt: "2026-09-24T09:01:00+09:00",
      }],
      backgroundResearch,
      shoppingPreferences: this.preferences,
    });
  }

  private allCandidateViews(): CandidateView[] {
    return [
      ...fixture.candidates.map(candidateToView),
      ...(this.findingStatus === "ADDED" ? [fixtureFindingCandidate()] : []),
    ];
  }

  private viewActiveCandidateIds(state: StoredWorkspace): string[] {
    return [
      ...state.activeCandidateIds,
      ...(this.findingStatus === "ADDED" ? ["fixture-imported-candidate"] : []),
    ];
  }

  private validateAnswer(question: FixtureQuestion, answer: QuestionAnswerInput): void {
    const customText = answer.customText?.trim() ?? "";
    if (answer.disposition === "no_preference") {
      if (!question.optional || answer.selectedOptionIds.length > 0 || customText.length > 0) {
        throw new FixtureDomainError("INVALID_ANSWER", "Only optional questions allow no preference");
      }
      return;
    }
    if (answer.selectedOptionIds.length > 1) {
      throw new FixtureDomainError("INVALID_ANSWER", "This fixture supports a single selection");
    }
    if (customText.length > 0 && !question.allowCustom) {
      throw new FixtureDomainError("INVALID_ANSWER", "This question does not allow a custom answer");
    }
    if (customText.length > 0 && answer.selectedOptionIds.length > 0) {
      throw new FixtureDomainError("INVALID_ANSWER", "Choose an option or enter a custom answer, not both");
    }
    if (answer.selectedOptionIds.length === 0 && (!question.allowCustom || customText.length === 0)) {
      throw new FixtureDomainError("INVALID_ANSWER", "Choose an option or enter a custom answer");
    }
    const validOptionIds = new Set(question.options.map((option) => option.id));
    if (answer.selectedOptionIds.some((optionId) => !validOptionIds.has(optionId))) {
      throw new FixtureDomainError("INVALID_ANSWER", "The selected option does not belong to this question");
    }
  }

  private upsertCondition(
    conditions: readonly ConditionChipView[],
    condition: ConditionChipView,
  ): ConditionChipView[] {
    return [
      ...conditions.filter((existing) => existing.id !== condition.id),
      condition,
    ];
  }

  private requireWorkspace(workspaceId: string): StoredWorkspace {
    const state = this.requireAnyWorkspace();
    if (state.id !== workspaceId) {
      throw new FixtureDomainError("NOT_FOUND", `Fixture workspace not found: ${workspaceId}`);
    }
    return state;
  }

  private requireAnyWorkspace(): StoredWorkspace {
    if (!this.workspace) {
      throw new FixtureDomainError("NOT_FOUND", "No fixture workspace has been created");
    }
    return this.workspace;
  }

  private assertRevision(actual: number, expected: number): void {
    if (actual !== expected) {
      throw new FixtureDomainError(
        "REVISION_CONFLICT",
        `Command revision ${actual} does not match current revision ${expected}`,
      );
    }
  }

  private async mutateOnce<T>(
    operation: string,
    context: CommandContext,
    payload: unknown,
    mutation: () => T,
  ): Promise<T> {
    const key = context.idempotencyKey.trim();
    if (key.length === 0) {
      throw new FixtureDomainError("IDEMPOTENCY_CONFLICT", "An idempotency key is required");
    }
    const fingerprint = JSON.stringify(payload);
    const receipt = this.receipts.get(key);
    if (receipt) {
      if (receipt.operation !== operation || receipt.fingerprint !== fingerprint) {
        throw new FixtureDomainError(
          "IDEMPOTENCY_CONFLICT",
          `Idempotency key ${key} was already used for a different fixture command`,
        );
      }
      return clone(receipt.result as T);
    }

    const result = mutation();
    this.receipts.set(key, {
      operation,
      fingerprint,
      result: clone(result),
    });
    return clone(result);
  }
}
