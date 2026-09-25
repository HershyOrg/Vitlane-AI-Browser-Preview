import type { BudgetLedger } from "./budget";
export type ControlMode = { mode: "AUTO" | "MANUAL"; version: number };
export type ActionEffect = { kind: string; targetId?: string; targetLabel?: string; before?: BudgetLedger | unknown; after?: BudgetLedger | unknown; count?: number };
// Round outcome a completed research Job reports (ADR-0086). Every number a response shows comes from here.
export type ResearchSourceFact = { source: string; status: string; reasonCode?: string; candidateCount: number };
export type ResearchFacts = { roundId: string; observed: number; duplicates: number; rejected: number; admitted: number; evaluated: number; unevaluated: number; sources: ResearchSourceFact[] };
export type ActionJob = { jobId: string; attemptId?: string; actionId: string; kind: string; targetId?: string; targetLabel?: string; status: string; effects: ActionEffect[]; reasonCode?: string; facts?: ResearchFacts };
// The Thread's natural-language reply. `[[ref]]` tokens in the body resolve through `references`, so a product name is never the model's own spelling.
export type ResponseReference = { ref: string; candidateId: string; targetId: string; title: string };
export type ThreadResponse = { schemaVersion: "vitlane.thread-response.v1" | "vitlane.thread-response.v2"; combination?: import("./combination").Combination; kind: "COMMENT" | "ANSWER"; body: string; locale: string; references: ResponseReference[]; modelKey?: string; createdAt: string };
export type ActionDecision = { id: string; kind: "ACTION" | "TARGET" | "CONDITIONS" | "BUDGET"; result: string; targetId?: string; targetLabel?: string; source: string; evidence?: string; reasonCode: string; actionIds: string[] };
export type ActionQuestion = { id: string; prompt: string; options: { id: string; label: string }[] };
export type CurationActionExecution = { id: string; threadId: string; sequence: number; type: string; targetId?: string; targetLabel?: string; status: "PENDING" | "RUNNING" | "WAITING_SELECTION" | "SUCCEEDED" | "FAILED" | "CANCELLED" | "SKIPPED"; generatedByActionId?: string; decisionIds: string[]; retryOfActionId?: string; jobs: ActionJob[]; effects: ActionEffect[]; decisions: ActionDecision[]; question?: ActionQuestion; questions?: ActionQuestion[]; answers: { revision: number; questionId: string; optionId?: string; text?: string }[]; reasonCode?: string; instruction?: string; response?: ThreadResponse };
export type CurationThread = { schemaVersion: "vitlane.curation-thread.v2"; id: string; curationId: string; retryOfThreadId?: string; retryOfJobId?: string; targetLabels?: Record<string, string>; mode: "AUTO" | "MANUAL"; origin: string; request: string; revision: number; status: string; actions: CurationActionExecution[]; reasonCode?: string; createdAt: string; updatedAt: string };
export const threadActive = (t: CurationThread) => ["INTERPRETING", "WAITING_SELECTION", "RUNNING"].includes(t.status);
export const currentAction = (t: CurationThread) => t.actions.find(a => ["PENDING", "RUNNING", "WAITING_SELECTION"].includes(a.status));

// The Server keeps the Thread status enum closed and reports a partial failure
// (some parallel Jobs succeeded, later Actions stopped) through this reason.
export const PARTIAL_FAILURE = "PARTIAL_FAILURE";
export type ThreadOutcome = "ACTIVE" | "SUCCEEDED" | "NO_RESULTS" | "SETTINGS_ONLY" | "ANSWERED" | "PARTIAL" | "FAILED" | "CANCELLED";
const researchActionTypes = ["START_RESEARCH", "PLANNING_START_CURATING", "TARGET_RESEARCH_AGAIN", "CURATION_ADD_TARGETS", "PLANNING_ADD_TARGETS", "INTENT_NEXT_STEP"];
export const threadJobs = (t: CurationThread): ActionJob[] => t.actions.flatMap(a => a.jobs);
export const researchJobs = (t: CurationThread) => threadJobs(t).filter(j => j.kind === "RESEARCH_ROUND");
export const failedJobs = (t: CurationThread) => threadJobs(t).filter(j => j.status === "FAILED");
export const candidateCount = (j: ActionJob) => j.effects.filter(e => e.kind === "CANDIDATES_ADDED").reduce((sum, e) => sum + (e.count ?? 0), 0);
export const targetLabelOf = (t: CurationThread, targetId?: string, fallback = "") => (targetId && t.targetLabels?.[targetId]) || fallback;
export function threadOutcome(t: CurationThread): ThreadOutcome {
 if (threadActive(t)) return "ACTIVE";
 if (t.status === "CANCELLED") return "CANCELLED";
 if (t.status === "FAILED") return t.reasonCode === PARTIAL_FAILURE ? "PARTIAL" : "FAILED";
 const research = researchJobs(t).filter(j => j.status === "SUCCEEDED");
 // A question ran no primitive: its only work is the reply, written or not.
 if (research.length === 0 && responseAction(t)?.instruction === "ANSWER") return "ANSWERED";
 if (research.length === 0) return t.actions.some(a => researchActionTypes.includes(a.type)) ? "SUCCEEDED" : "SETTINGS_ONLY";
 return research.every(j => candidateCount(j) === 0) ? "NO_RESULTS" : "SUCCEEDED";
}
// Candidates found per target across every completed research Job of the Thread.
export function candidateSummary(t: CurationThread, fallbackLabel: string) {
 const byTarget = new Map<string, { targetId: string; label: string; count: number }>();
 for (const j of researchJobs(t)) {
  if (j.status !== "SUCCEEDED") continue;
  const key = j.targetId ?? j.jobId;
  const entry = byTarget.get(key) ?? { targetId: key, label: j.targetLabel || targetLabelOf(t, j.targetId, fallbackLabel), count: 0 };
  entry.count += candidateCount(j);
  byTarget.set(key, entry);
 }
 return [...byTarget.values()];
}
export const RESPONSE_ACTION = "RESPONSE";
export const responseAction = (t: CurationThread) => t.actions.find(a => a.type === RESPONSE_ACTION);
export const threadResponse = (t: CurationThread) => responseAction(t)?.response;
export const threadProgress = (t: CurationThread) => ({ done: t.actions.filter(a => a.status === "SUCCEEDED").length, total: t.actions.length });
