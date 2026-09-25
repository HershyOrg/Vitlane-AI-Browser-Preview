import type { CandidateView, QuestionView, WorkspaceView } from "../domain";
import {
  candidateSurfaceKind,
  projectBlockingQuestionSurface,
  surfaceAssetRegistry,
} from "./surfaceRegistry";

const text = (value: string) => ({ "ko-KR": value, "en-US": value });

const question = (id: string, optional = false): QuestionView => ({
  id,
  revision: 1,
  title: text(`Question ${id}`),
  reason: text("Changes the result"),
  selection: "single",
  optional,
  allowCustom: true,
  options: [
    { id: "yes", title: text("Yes"), description: text("") },
    { id: "no", title: text("No"), description: text("") },
  ],
  state: "unanswered",
  selectedOptionIds: [],
});

describe("surface asset registry", () => {
  it("is a closed catalog of known reusable components", () => {
    expect(Object.keys(surfaceAssetRegistry)).toEqual([
      "question.choice",
      "question.free-text",
      "candidate.product",
      "candidate.service",
      "budget.adjustment",
      "feedback.receipt",
      "feedback.status",
      "goal.shopping",
      "activity.timeline",
      "idea.proposal",
      "monitor.research",
      "memory.shopping-preferences",
      "approval.external-navigation",
      "navigation.destination-search",
      "run.overview",
      "run.browser-status",
      "run.browser-session",
      "activity.run-audit",
      "handoff.login",
      "handoff.verification",
      "approval.checkout",
      "handoff.final-action",
      "result.run",
      "handoff.payment",
    ]);
  });

  it("projects only the canonical blocking question", () => {
    const workspace = {
      activeQuestionId: "blocking",
      questions: [question("optional", true), question("blocking")],
    } as unknown as WorkspaceView;

    expect(projectBlockingQuestionSurface(workspace, "ko-KR")).toEqual(expect.objectContaining({
      kind: "question.choice",
      questionId: "blocking",
      title: "Question blocking",
    }));
    expect(projectBlockingQuestionSurface({ ...workspace, activeQuestionId: null }, "ko-KR")).toBeNull();
  });

  it("derives candidate surface kinds locally instead of trusting wire component names", () => {
    const candidate = {
      id: "candidate",
      itemIds: ["delivery", "pickup"],
    } as unknown as CandidateView;

    expect(candidateSurfaceKind(candidate, new Set(["pickup"]))).toBe("candidate.service");
    expect(candidateSurfaceKind(candidate, new Set())).toBe("candidate.product");
  });
});
