import { candidateCount, researchJobs, threadActive, type CurationThread, type ResearchFacts, type ResearchSourceFact } from "./thread";

/**
 * The finished Thread whose research last added candidates to each Target (else the last that
 * researched it). A representative the reader opened before that research is no longer the
 * newest signal; every row of the Target reads the same time from here.
 */
export function liveResultHolders(threads: readonly CurationThread[], targetIds: readonly string[]): Record<string, string | undefined> {
  const ordered = [...threads].filter(t => !threadActive(t)).sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  const added: Record<string, string | undefined> = {};
  const researched: Record<string, string | undefined> = {};
  for (const thread of ordered) {
    for (const job of researchJobs(thread)) {
      if (job.status !== "SUCCEEDED" || !job.targetId) continue;
      researched[job.targetId] = thread.id;
      if (candidateCount(job) > 0) added[job.targetId] = thread.id;
    }
  }
  return Object.fromEntries(targetIds.map(id => [id, added[id] ?? researched[id]]));
}

/** The product groups a request works on: the ones it added and the ones it researched, whatever became of them. */
export function threadTargetIds(thread: CurationThread): string[] {
  const ids = new Set<string>();
  for (const action of thread.actions) {
    for (const effect of [...action.effects, ...action.jobs.flatMap(job => job.effects)]) if (effect.kind === "TARGET_ADDED" && effect.targetId) ids.add(effect.targetId);
    if (action.type === "TARGET_RESEARCH_AGAIN" && action.targetId) ids.add(action.targetId);
    for (const job of action.jobs) if (job.kind === "RESEARCH_ROUND" && job.targetId) ids.add(job.targetId);
  }
  return [...ids];
}

/**
 * One group of rows per request turn (ADR-0089): a turn shows the product groups it added or
 * researched from the moment it knows them — while it still runs — and keeps them after a later
 * request researches the same group again, so nothing above the reader moves. Groups keep the
 * curation's order inside a turn; product groups no request worked on come last, under `undefined`.
 *
 * `targetCreatedAt` closes one gap: the page reads the product groups and the requests separately, so a
 * group a running request has just made can arrive before that request's record of it. Such a group
 * belongs to the latest request that started before it, while that request still runs — it appears in
 * that turn at once instead of at the end first and moving into the turn a moment later.
 */
export function turnResultGroups(threads: readonly CurationThread[], targetIds: readonly string[], targetCreatedAt: Readonly<Record<string, string | undefined>> = {}): Array<{ holderId?: string; targetIds: string[] }> {
  const groups: Array<{ holderId?: string; targetIds: string[] }> = [];
  const held = new Set<string>();
  const ordered = [...threads].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt));
  const recorded = new Set(ordered.flatMap(threadTargetIds));
  const claimed = new Map<string, string>();
  for (const id of targetIds) {
    const created = Date.parse(targetCreatedAt[id] ?? "");
    if (recorded.has(id) || Number.isNaN(created)) continue;
    const owner = ordered.filter(thread => Date.parse(thread.createdAt) <= created).at(-1);
    if (owner && threadActive(owner)) claimed.set(id, owner.id);
  }
  for (const thread of ordered) {
    const own = new Set(threadTargetIds(thread));
    const ids = targetIds.filter(id => own.has(id) || claimed.get(id) === thread.id);
    if (ids.length === 0) continue;
    groups.push({ holderId: thread.id, targetIds: ids });
    for (const id of ids) held.add(id);
  }
  const unheld = targetIds.filter(id => !held.has(id));
  return unheld.length > 0 ? [...groups, { holderId: undefined, targetIds: unheld }] : groups;
}

export type ComparisonFacts = { observed: number; compared: number; duplicates: number; rejected: number; admitted: number; unevaluated: number; sources: ResearchSourceFact[] };

/** What this response's research looked at for the given Targets. Undefined when the Server reported no Round outcome (older Threads). */
export function comparisonFacts(thread: CurationThread, targetIds: readonly string[]): ComparisonFacts | undefined {
  const facts: ResearchFacts[] = researchJobs(thread).flatMap(job => job.status === "SUCCEEDED" && job.facts && job.targetId && targetIds.includes(job.targetId) ? [job.facts] : []);
  if (facts.length === 0) return undefined;
  const sources = new Map<string, ResearchSourceFact>();
  for (const fact of facts) for (const source of fact.sources) {
    const merged = sources.get(source.source);
    // A source that answered anywhere counts as answered; its skip reason is kept only while it never did.
    if (!merged) sources.set(source.source, { ...source });
    else sources.set(source.source, { source: source.source, candidateCount: merged.candidateCount + source.candidateCount, status: merged.candidateCount + source.candidateCount > 0 ? "SUCCEEDED" : merged.status, reasonCode: merged.candidateCount + source.candidateCount > 0 ? undefined : merged.reasonCode ?? source.reasonCode });
  }
  const sum = (key: "observed" | "evaluated" | "duplicates" | "rejected" | "admitted" | "unevaluated") => facts.reduce((total, fact) => total + fact[key], 0);
  return { observed: sum("observed"), compared: sum("evaluated"), duplicates: sum("duplicates"), rejected: sum("rejected"), admitted: sum("admitted"), unevaluated: sum("unevaluated"),
    sources: [...sources.values()].sort((a, b) => b.candidateCount - a.candidateCount || a.source.localeCompare(b.source)) };
}

/**
 * What the reader last did inside a Target, kept on this device. Opening (or pinning) a product and
 * choosing a sort are both ways of saying "this is what I am looking at"; the later one wins.
 */
export type RepresentativeSignal = { kind: "VIEWED"; candidateId: string; at: string } | { kind: "SORT"; at: string };
export type RepresentativeReason = "CART" | "VIEWED" | "SORT" | "PICK" | "COMBINATION";
export type Representative<T> = { candidate: T; reason: RepresentativeReason };

/**
 * The one product a response shows for a Target (ADR-0086). `ordered` is the Target's eligible
 * candidates — visible, not disliked, details resolved — in the sort the reader chose, so its first
 * entry is that sort's leader (Vitlane Pick by default).
 *
 * 1. A carted product stays: the row doubles as "this product group is settled".
 * 2. Otherwise the product the reader last opened, unless a sort was chosen or a research added
 *    candidates after it: both are newer statements about what to look at, and a fresh research must
 *    agree with the reply written for it.
 * 3. Otherwise the leader of the chosen sort.
 */
export function chooseRepresentative<T extends { candidateId: string }>(input: {
  ordered: readonly T[]; inCart: (candidateId: string) => boolean; pickSort: boolean; signal?: RepresentativeSignal; researchedAt?: string; combination?: boolean;
}): Representative<T> | undefined {
  const leader = input.ordered[0];
  if (!leader) return undefined;
  const carted = input.ordered.find(c => input.inCart(c.candidateId));
  if (carted && !input.combination) return { candidate: carted, reason: "CART" };
  const signal = input.signal;
  // Instants, not strings: the Server stamps its own offset (+09:00) and the device stamps UTC.
  if (signal?.kind === "VIEWED" && !(input.researchedAt && Date.parse(input.researchedAt) > Date.parse(signal.at))) {
    const viewed = input.ordered.find(c => c.candidateId === signal.candidateId);
    // Looking at the leader changes nothing: it stays "Pick" (or the sort's leader), the more telling reason.
    if (viewed && viewed.candidateId !== leader.candidateId) return { candidate: viewed, reason: "VIEWED" };
  }
  return { candidate: leader, reason: input.pickSort ? "PICK" : "SORT" };
}

/**
 * A product name as it reads inside a sentence. Listing titles lead with store tags ("[11번가] [정품] …")
 * and run long; the full title stays on the row below and in the link's label.
 */
export function shortProductName(title: string, limit = 22): string {
  const stripped = title.replace(/^(?:\s*[\[(【][^\])】]{1,16}[\])】])+\s*/u, "").trim() || title.trim();
  const characters = Array.from(stripped);
  if (characters.length <= limit) return stripped;
  const clipped = characters.slice(0, limit).join("");
  const boundary = clipped.lastIndexOf(" ");
  return `${(boundary >= limit / 2 ? clipped.slice(0, boundary) : clipped).trimEnd()}…`;
}
