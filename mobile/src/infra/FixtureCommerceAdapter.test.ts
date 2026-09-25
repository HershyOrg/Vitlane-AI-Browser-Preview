import { FixtureCommerceAdapter } from "./FixtureCommerceAdapter";

const intent = "10월 3일에 친구 6명이랑 집에서 파티할 거야. 15만 원 안으로 준비해줘.";

async function createAdapter(): Promise<{
  adapter: FixtureCommerceAdapter;
  workspaceId: string;
}> {
  const adapter = new FixtureCommerceAdapter();
  const workspace = await adapter.createWorkspace(intent, {
    idempotencyKey: "create-party",
    baseRevision: 0,
  });
  if (!workspace) {
    throw new Error("Expected fixture workspace");
  }
  return { adapter, workspaceId: workspace.id };
}

async function chooseGoodsOnly(
  adapter: FixtureCommerceAdapter,
  workspaceId: string,
  keySuffix: string,
) {
  return adapter.answerQuestion(workspaceId, {
    questionId: "q-service",
    selectedOptionIds: ["goods-only"],
    disposition: "apply",
  }, { idempotencyKey: `service-${keySuffix}`, baseRevision: 1 });
}

describe("FixtureCommerceAdapter", () => {
  it("does not create a workspace for a blank intent", async () => {
    const adapter = new FixtureCommerceAdapter();

    await expect(adapter.createWorkspace("   ", {
      idempotencyKey: "blank-intent",
      baseRevision: 0,
    })).resolves.toBeNull();

    await expect(adapter.getWorkspace("demo-party")).rejects.toMatchObject({
      code: "NOT_FOUND",
    });
  });

  it("creates a complete plan immediately and marks fixture defaults as assumptions", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const workspace = await adapter.getWorkspace(workspaceId);

    expect(workspace.revision).toBe(1);
    expect(workspace.activeQuestionId).toBeNull();
    expect(workspace.plan.totals.knownTotal.amount).toBe(124000);
    expect(workspace.selectedCandidateIds).toEqual(["party-balanced"]);
    expect(workspace.answers).toEqual(expect.arrayContaining([
      expect.objectContaining({
        questionId: "q-region",
        selectedOptionIds: ["mapo"],
        origin: "assumed",
      }),
      expect.objectContaining({
        questionId: "q-service",
        selectedOptionIds: ["include"],
        origin: "assumed",
      }),
      expect.objectContaining({
        questionId: "q-slot",
        selectedOptionIds: ["1600"],
        origin: "assumed",
      }),
    ]));
    expect(workspace.questions).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: "q-region", answerOrigin: "assumed" }),
      expect.objectContaining({ id: "q-service", answerOrigin: "assumed" }),
      expect.objectContaining({ id: "q-slot", answerOrigin: "assumed" }),
    ]));
    expect(workspace.conditions.filter((condition) => !condition.confirmed)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: "region", refinementQuestionId: "q-region" }),
        expect.objectContaining({ id: "service", refinementQuestionId: "q-service" }),
        expect.objectContaining({ id: "pickup-time", refinementQuestionId: "q-slot" }),
      ]),
    );
  });

  it("starts the review monitor only after the user accepts its proposal", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const initial = await adapter.getWorkspace(workspaceId);

    expect(initial.agentMessages?.[0]).toEqual(expect.objectContaining({
      id: "fixture-proposal-deals",
      status: "PENDING",
    }));
    expect(initial.backgroundResearch?.subscriptions).toEqual([]);
    expect(initial.backgroundResearch?.findings).toEqual([]);

    const accepted = await adapter.respondToAgentMessage(
      workspaceId,
      "fixture-proposal-deals",
      1,
      "ACCEPT",
      { idempotencyKey: "accept-review-monitor", baseRevision: initial.revision },
    );

    expect(accepted.agentMessages?.[0]?.status).toBe("ACCEPTED");
    expect(accepted.backgroundResearch?.subscriptions).toEqual([
      expect.objectContaining({ id: "fixture-subscription", status: "ACTIVE" }),
    ]);
    expect(accepted.backgroundResearch?.findings).toEqual([
      expect.objectContaining({ id: "fixture-finding", status: "NEW" }),
    ]);

    const imported = await adapter.importResearchFinding(workspaceId, "fixture-finding");
    expect(imported.backgroundResearch?.findings[0]).toEqual(expect.objectContaining({
      status: "ADDED",
      candidateId: "fixture-imported-candidate",
    }));
    expect(imported.activeCandidateIds).toContain("fixture-imported-candidate");
    expect(imported.candidates).toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: "fixture-imported-candidate",
        price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 29_900 } },
      }),
    ]));
  });

  it("applies an answer once for a duplicated idempotency key and stores no draft", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const command = {
      idempotencyKey: "answer-region",
      baseRevision: 1,
    };
    const answer = {
      questionId: "q-region",
      selectedOptionIds: ["mapo"],
      disposition: "apply" as const,
    };

    const first = await adapter.answerQuestion(workspaceId, answer, command);
    const duplicate = await adapter.answerQuestion(workspaceId, answer, command);
    const current = await adapter.getWorkspace(workspaceId);

    expect(first.revision).toBe(2);
    expect(duplicate.revision).toBe(2);
    expect(current.revision).toBe(2);
    expect(current.answers).toHaveLength(3);
    expect(current.answers.find((answer) => answer.questionId === "q-region")).toMatchObject({
      selectedOptionIds: ["mapo"],
      committedAtRevision: 2,
      origin: "user",
    });
    expect(current).not.toHaveProperty("questionDraft");
    expect(current.activeQuestionId).toBeNull();
  });

  it("keeps the KRW 124,000 plan unchanged until the KRW 80,000 preview is applied", async () => {
    const { adapter, workspaceId } = await createAdapter();

    const preview = await adapter.previewBudget(workspaceId, 80000, {
      idempotencyKey: "preview-budget-80",
      baseRevision: 1,
    });
    const beforeApply = await adapter.getWorkspace(workspaceId);

    expect(preview.projectedPlan.totals.subtotal.amount).toBe(70000);
    expect(preview.projectedPlan.totals.knownTotal.amount).toBe(75000);
    expect(preview.projectedPlan.totals.remainingBudget.amount).toBe(5000);
    expect(beforeApply.revision).toBe(1);
    expect(beforeApply.plan.totals.knownTotal.amount).toBe(124000);
    expect(beforeApply.plan.totals.budget.amount).toBe(150000);

    const applied = await adapter.applyBudgetPreview(workspaceId, preview.id, {
      idempotencyKey: "apply-budget-80",
      baseRevision: 1,
    });
    const duplicate = await adapter.applyBudgetPreview(workspaceId, preview.id, {
      idempotencyKey: "apply-budget-80",
      baseRevision: 1,
    });

    expect(applied.revision).toBe(2);
    expect(applied.plan.totals.knownTotal.amount).toBe(75000);
    expect(applied.plan.totals.budget.amount).toBe(80000);
    expect(applied.plan.items.find((item) => item.id === "decor")?.state).toBe("removed");
    expect(duplicate.revision).toBe(2);
    await expect(adapter.getWorkspace(workspaceId)).resolves.toMatchObject({ revision: 2 });
  });

  it("keeps the budget candidate when pickup is confirmed after the budget change", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const preview = await adapter.previewBudget(workspaceId, 80000, {
      idempotencyKey: "preview-before-service-confirmation",
      baseRevision: 1,
    });
    const budget = await adapter.applyBudgetPreview(workspaceId, preview.id, {
      idempotencyKey: "apply-before-service-confirmation",
      baseRevision: 1,
    });

    const confirmed = await adapter.answerQuestion(workspaceId, {
      questionId: "q-service",
      selectedOptionIds: ["include"],
      disposition: "apply",
    }, {
      idempotencyKey: "confirm-service-after-budget",
      baseRevision: budget.revision,
    });

    expect(confirmed.revision).toBe(3);
    expect(confirmed.plan.totals.knownTotal.amount).toBe(75000);
    expect(confirmed.activeCandidateIds).toEqual(["party-budget"]);
    expect(confirmed.selectedCandidateIds).toEqual(["party-budget"]);
  });

  it("commits independent refinements one revision at a time without opening another question", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const region = await adapter.answerQuestion(workspaceId, {
      questionId: "q-region",
      selectedOptionIds: ["mapo"],
      disposition: "apply",
    }, { idempotencyKey: "region", baseRevision: 1 });
    const service = await adapter.answerQuestion(workspaceId, {
      questionId: "q-service",
      selectedOptionIds: ["include"],
      disposition: "apply",
    }, { idempotencyKey: "service", baseRevision: region.revision });
    const slot = await adapter.answerQuestion(workspaceId, {
      questionId: "q-slot",
      selectedOptionIds: ["1700"],
      disposition: "apply",
    }, { idempotencyKey: "slot", baseRevision: service.revision });

    expect(region.activeQuestionId).toBeNull();
    expect(service.activeQuestionId).toBeNull();
    expect(slot.revision).toBe(service.revision + 1);
    expect(slot.activeQuestionId).toBeNull();
    expect(slot.plan.items.find((item) => item.id === "cake")?.slot?.start).toContain("T17:00:00");
    expect(slot.answers.find((answer) => answer.questionId === "q-slot")).toMatchObject({
      selectedOptionIds: ["1700"],
      origin: "user",
    });
  });

  it("changes the visible candidate set when the user chooses goods only", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const goodsOnly = await chooseGoodsOnly(adapter, workspaceId, "goods-flow");
    const candidates = await adapter.getCandidates(workspaceId);

    expect(goodsOnly.revision).toBe(2);
    expect(goodsOnly.activeCandidateIds).toEqual(["party-goods-only"]);
    expect(goodsOnly.plan.items.find((item) => item.id === "cake")?.state).toBe("removed");
    expect(goodsOnly.conditions.some((condition) => condition.id === "pickup-time")).toBe(false);
    expect(goodsOnly.conditions.find((condition) => condition.id === "service")).toMatchObject({
      confirmed: true,
      refinementQuestionId: "q-service",
    });
    expect(goodsOnly.plan.totals.knownTotal.amount).toBe(82000);
    expect(candidates.map((candidate) => candidate.id)).toEqual(["party-goods-only"]);
    expect(goodsOnly.followUpSuggestions.map((suggestion) => suggestion.id)).toEqual([
      "remove-decor",
    ]);
  });

  it("preserves goods-only exclusions in an 80,000 won budget preview and candidate", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const goodsOnly = await chooseGoodsOnly(adapter, workspaceId, "goods-budget");

    const preview = await adapter.previewBudget(workspaceId, 80000, {
      idempotencyKey: "preview-goods-budget",
      baseRevision: goodsOnly.revision,
    });

    expect(preview.projectedPlan.items.find((item) => item.id === "cake")?.state).toBe("removed");
    expect(preview.projectedPlan.items.find((item) => item.id === "decor")?.state).toBe("removed");
    expect(preview.projectedPlan.totals.knownTotal.amount).toBe(47000);
    expect(preview.changes.map((change) => change.itemId)).toEqual([
      "snacks",
      "drinks",
      "decor",
    ]);

    const applied = await adapter.applyBudgetPreview(workspaceId, preview.id, {
      idempotencyKey: "apply-goods-budget",
      baseRevision: goodsOnly.revision,
    });
    const candidates = await adapter.getCandidates(workspaceId);

    expect(applied.selectedCandidateIds).toEqual(["party-goods-only-budget"]);
    expect(applied.activeCandidateIds).toEqual(["party-goods-only-budget"]);
    expect(applied.plan.totals.knownTotal.amount).toBe(47000);
    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      id: "party-goods-only-budget",
      price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 47000 } },
      itemIds: ["snacks", "drinks"],
    });
  });

  it("supports removing decor from goods-only and rejects pickup or repeated no-op edits", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const goodsOnly = await chooseGoodsOnly(adapter, workspaceId, "goods-follow-up");

    await expect(adapter.submitFollowUp(
      workspaceId,
      "케이크 픽업은 오후 5시로 바꿔줘",
      { idempotencyKey: "goods-pickup-rejected", baseRevision: goodsOnly.revision },
    )).rejects.toMatchObject({ code: "UNSUPPORTED_FIXTURE_CHANGE" });

    const withoutDecor = await adapter.submitFollowUp(
      workspaceId,
      "장식은 빼줘",
      { idempotencyKey: "goods-remove-decor", baseRevision: goodsOnly.revision },
    );
    const candidates = await adapter.getCandidates(workspaceId);

    expect(withoutDecor.plan.totals.knownTotal.amount).toBe(65000);
    expect(withoutDecor.selectedCandidateIds).toEqual(["party-goods-only-light"]);
    expect(candidates[0]).toMatchObject({
      id: "party-goods-only-light",
      price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 65000 } },
      itemIds: ["snacks", "drinks"],
    });
    expect(withoutDecor.followUpSuggestions).toEqual([]);

    await expect(adapter.submitFollowUp(
      workspaceId,
      "장식은 빼줘",
      { idempotencyKey: "goods-remove-decor-again", baseRevision: withoutDecor.revision },
    )).rejects.toMatchObject({ code: "UNSUPPORTED_FIXTURE_CHANGE" });
    await expect(adapter.getWorkspace(workspaceId)).resolves.toMatchObject({
      revision: withoutDecor.revision,
    });
  });

  it("rejects custom pickup slots and option-plus-custom region answers", async () => {
    const { adapter, workspaceId } = await createAdapter();

    await expect(adapter.answerQuestion(workspaceId, {
      questionId: "q-region",
      selectedOptionIds: ["mapo"],
      customText: "서울 성동구",
      disposition: "apply",
    }, { idempotencyKey: "region-option-and-custom", baseRevision: 1 })).rejects.toMatchObject({
      code: "INVALID_ANSWER",
    });

    const customRegion = await adapter.answerQuestion(workspaceId, {
      questionId: "q-region",
      selectedOptionIds: [],
      customText: "서울 성동구",
      disposition: "apply",
    }, { idempotencyKey: "custom-region", baseRevision: 1 });
    const service = await adapter.answerQuestion(workspaceId, {
      questionId: "q-service",
      selectedOptionIds: ["include"],
      disposition: "apply",
    }, { idempotencyKey: "include-service-custom-test", baseRevision: customRegion.revision });

    await expect(adapter.answerQuestion(workspaceId, {
      questionId: "q-slot",
      selectedOptionIds: [],
      customText: "오후 6시",
      disposition: "apply",
    }, { idempotencyKey: "custom-slot", baseRevision: service.revision })).rejects.toMatchObject({
      code: "INVALID_ANSWER",
    });
    await expect(adapter.getWorkspace(workspaceId)).resolves.toMatchObject({
      revision: service.revision,
      activeQuestionId: null,
    });
  });

  it("rejects a pickup-time refinement after cake has been removed", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const goodsOnly = await chooseGoodsOnly(adapter, workspaceId, "slot-rejected");

    await expect(adapter.answerQuestion(workspaceId, {
      questionId: "q-slot",
      selectedOptionIds: ["1700"],
      disposition: "apply",
    }, {
      idempotencyKey: "slot-after-cake-removed",
      baseRevision: goodsOnly.revision,
    })).rejects.toMatchObject({ code: "UNSUPPORTED_FIXTURE_CHANGE" });

    await expect(adapter.getWorkspace(workspaceId)).resolves.toMatchObject({
      revision: goodsOnly.revision,
      activeQuestionId: null,
    });
  });

  it("restores the user's pickup slot when pickup is re-included and remains idempotent", async () => {
    const { adapter, workspaceId } = await createAdapter();
    const slot = await adapter.answerQuestion(workspaceId, {
      questionId: "q-slot",
      selectedOptionIds: ["1700"],
      disposition: "apply",
    }, { idempotencyKey: "choose-five", baseRevision: 1 });
    const goodsOnly = await adapter.answerQuestion(workspaceId, {
      questionId: "q-service",
      selectedOptionIds: ["goods-only"],
      disposition: "apply",
    }, { idempotencyKey: "remove-pickup", baseRevision: slot.revision });
    const includeAnswer = {
      questionId: "q-service",
      selectedOptionIds: ["include"],
      disposition: "apply" as const,
    };
    const includeCommand = {
      idempotencyKey: "restore-pickup",
      baseRevision: goodsOnly.revision,
    };

    const restored = await adapter.answerQuestion(workspaceId, includeAnswer, includeCommand);
    const duplicate = await adapter.answerQuestion(workspaceId, includeAnswer, includeCommand);

    expect(restored.revision).toBe(goodsOnly.revision + 1);
    expect(duplicate.revision).toBe(restored.revision);
    expect(restored.activeQuestionId).toBeNull();
    expect(restored.plan.items.find((item) => item.id === "cake")).toMatchObject({
      state: "selected",
      slot: expect.objectContaining({ start: expect.stringContaining("T17:00:00") }),
    });
    expect(restored.conditions.find((condition) => condition.id === "pickup-time")).toMatchObject({
      confirmed: true,
      value: expect.objectContaining({ "ko-KR": "오후 5시", "en-US": "5 PM" }),
      refinementQuestionId: "q-slot",
    });
    expect(restored.answers.find((answer) => answer.questionId === "q-slot")).toMatchObject({
      selectedOptionIds: ["1700"],
      origin: "user",
    });
  });

  it("changes the pickup slot and removes decorations in one deterministic follow-up", async () => {
    const { adapter, workspaceId } = await createAdapter();

    const updated = await adapter.submitFollowUp(
      workspaceId,
      "케이크 픽업은 오후 5시로 바꾸고 장식은 빼줘",
      { idempotencyKey: "follow-up-slot-decor", baseRevision: 1 },
    );
    const duplicate = await adapter.submitFollowUp(
      workspaceId,
      "케이크 픽업은 오후 5시로 바꾸고 장식은 빼줘",
      { idempotencyKey: "follow-up-slot-decor", baseRevision: 1 },
    );

    expect(updated.revision).toBe(2);
    expect(updated.plan.items.find((item) => item.id === "cake")?.slot?.start).toContain("T17:00:00");
    expect(updated.plan.items.find((item) => item.id === "decor")?.state).toBe("removed");
    expect(updated.plan.totals.knownTotal.amount).toBe(107000);
    expect(updated.selectedCandidateIds).toEqual(["party-light"]);
    expect(duplicate.revision).toBe(2);
  });

  it("rejects reuse of an idempotency key for a different mutation", async () => {
    const { adapter, workspaceId } = await createAdapter();
    await adapter.answerQuestion(workspaceId, {
      questionId: "q-region",
      selectedOptionIds: ["mapo"],
      disposition: "apply",
    }, { idempotencyKey: "one-key", baseRevision: 1 });

    await expect(adapter.answerQuestion(workspaceId, {
      questionId: "q-region",
      selectedOptionIds: [],
      customText: "서울 성동구",
      disposition: "apply",
    }, { idempotencyKey: "one-key", baseRevision: 2 })).rejects.toMatchObject({
      code: "IDEMPOTENCY_CONFLICT",
    });
  });
});
