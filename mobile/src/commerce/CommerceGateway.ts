import type {
  AgentMessageResponse,
  BudgetChangePreview,
  BrowserRunStartContext,
  BrowserRunView,
  CandidateView,
  CommandContext,
  PlanView,
  QuestionAnswerInput,
  ShoppingPreferencesPatch,
  WorkspaceView,
} from "../domain";

/**
 * UI-facing commerce port.
 *
 * Implementations may use deterministic review data or the Vitlane server,
 * but screens never depend on either transport. `workspaceId` is the
 * curation identifier for the server implementation.
 */
export interface CommerceGateway {
  readonly mode: "fixture" | "server";

  createWorkspace(
    intent: string,
    context: CommandContext,
  ): Promise<WorkspaceView | null>;
  getWorkspace(workspaceId: string): Promise<WorkspaceView>;
  getPlan(planId: string): Promise<PlanView>;
  getCandidates(workspaceId: string): Promise<readonly CandidateView[]>;
  startBrowserRun(
    workspaceId: string,
    candidateId: string,
    context: BrowserRunStartContext,
  ): Promise<BrowserRunView>;
  answerQuestion(
    workspaceId: string,
    answer: QuestionAnswerInput,
    context: CommandContext,
  ): Promise<WorkspaceView>;
  previewBudget(
    workspaceId: string,
    budgetAmount: number,
    context: CommandContext,
  ): Promise<BudgetChangePreview>;
  applyBudgetPreview(
    workspaceId: string,
    previewId: string,
    context: CommandContext,
  ): Promise<WorkspaceView>;
  submitFollowUp(
    workspaceId: string,
    text: string,
    context: CommandContext,
  ): Promise<WorkspaceView>;
  respondToAgentMessage(
    workspaceId: string,
    messageId: string,
    messageVersion: number,
    response: AgentMessageResponse,
    context: CommandContext,
  ): Promise<WorkspaceView>;
  cancelResearchSubscription(
    workspaceId: string,
    subscriptionId: string,
  ): Promise<WorkspaceView>;
  hideResearchFinding(
    workspaceId: string,
    findingId: string,
  ): Promise<WorkspaceView>;
  importResearchFinding(
    workspaceId: string,
    findingId: string,
  ): Promise<WorkspaceView>;
  updateShoppingPreferences(
    workspaceId: string,
    patch: ShoppingPreferencesPatch,
  ): Promise<WorkspaceView>;
}
