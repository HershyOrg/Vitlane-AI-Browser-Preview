import type {
  CandidateView,
  Locale,
  QuestionView,
  WorkspaceView,
} from "../domain";

/**
 * Closed UI asset catalog. Wire data is projected into one of these local
 * kinds; a server or model never supplies a React component name or raw props.
 */
export const surfaceAssetRegistry = Object.freeze({
  "question.choice": {
    component: "BlockingQuestionCard",
    editor: "ClarificationSheet",
    contentModel: "QuestionView",
  },
  "question.free-text": {
    component: "BlockingQuestionCard",
    editor: "ClarificationSheet",
    contentModel: "QuestionView",
  },
  "candidate.product": {
    component: "CandidateCard",
    contentModel: "CandidateView",
  },
  "candidate.service": {
    component: "CandidateCard",
    contentModel: "CandidateView",
  },
  "budget.adjustment": {
    component: "BudgetSheet",
    contentModel: "BudgetChangePreview",
  },
  "feedback.receipt": {
    component: "WorkspaceReceipt",
    contentModel: "LocalizedText[]",
  },
  "feedback.status": {
    component: "StatusNotice",
    contentModel: "LocalizedText",
  },
  "goal.shopping": {
    component: "ShoppingGoalCard",
    contentModel: "ShoppingGoalPresentation",
  },
  "activity.timeline": {
    component: "AgentActivitySheet",
    contentModel: "AgentActivityPresentation[]",
  },
  "idea.proposal": {
    component: "ShoppingProposalCard",
    contentModel: "ShoppingProposalPresentation",
  },
  "monitor.research": {
    component: "BackgroundResearchPanel",
    contentModel: "BackgroundResearchPresentation",
  },
  "memory.shopping-preferences": {
    component: "ShoppingMemorySheet",
    contentModel: "ShoppingMemoryValue",
  },
  "approval.external-navigation": {
    component: "ExternalLinkApprovalSheet",
    contentModel: "ExternalLinkApprovalPresentation",
  },
  "navigation.destination-search": {
    component: "BrowserDestinationLauncher",
    contentModel: "BrowserDestinationTarget[]",
  },
  "run.overview": {
    component: "BrowserRunPanel",
    contentModel: "BrowserRunPresentation",
  },
  "run.browser-status": {
    component: "BrowserRunSheet",
    contentModel: "BrowserRunPresentation",
  },
  "run.browser-session": {
    component: "BrowserSessionSurface",
    contentModel: "BrowserSessionPresentation",
  },
  "activity.run-audit": {
    component: "AgentActivitySheet",
    contentModel: "AgentActivityPresentation[]",
  },
  "handoff.login": {
    component: "HumanHandoffSheet",
    contentModel: "HumanHandoffPresentation",
  },
  "handoff.verification": {
    component: "HumanHandoffSheet",
    contentModel: "HumanHandoffPresentation",
  },
  "approval.checkout": {
    component: "HumanHandoffSheet",
    contentModel: "HumanHandoffPresentation",
  },
  "handoff.final-action": {
    component: "HumanHandoffSheet",
    contentModel: "HumanHandoffPresentation",
  },
  "result.run": {
    component: "RunResultCard",
    contentModel: "RunResultPresentation",
  },
  "handoff.payment": {
    component: "BrowserSessionSurface",
    contentModel: "BrowserSessionPresentation",
  },
} as const);

export type ApprovedSurfaceKind = keyof typeof surfaceAssetRegistry;

export type BlockingQuestionSurface = {
  readonly kind: "question.choice" | "question.free-text";
  readonly questionId: string;
  readonly title: string;
  readonly reason: string;
  readonly options: readonly { readonly id: string; readonly label: string }[];
  readonly allowCustom: boolean;
};

/** Only the canonical active question is allowed to interrupt the flow. */
export function projectBlockingQuestionSurface(
  workspace: WorkspaceView,
  locale: Locale,
): BlockingQuestionSurface | null {
  if (!workspace.activeQuestionId) return null;
  const question = workspace.questions.find((item) => (
    item.id === workspace.activeQuestionId && item.state === "unanswered"
  ));
  if (!question) return null;
  return questionSurface(question, locale);
}

function questionSurface(question: QuestionView, locale: Locale): BlockingQuestionSurface {
  const compactChoice = question.options.length >= 2 && question.options.length <= 3;
  return {
    kind: compactChoice ? "question.choice" : "question.free-text",
    questionId: question.id,
    title: question.title[locale],
    reason: question.reason[locale],
    options: question.options.map((option) => ({
      id: option.id,
      label: option.title[locale],
    })),
    allowCustom: question.allowCustom,
  };
}

export function candidateSurfaceKind(
  candidate: CandidateView,
  serviceItemIds: ReadonlySet<string>,
): "candidate.product" | "candidate.service" {
  return candidate.itemIds.some((itemId) => serviceItemIds.has(itemId))
    ? "candidate.service"
    : "candidate.product";
}
