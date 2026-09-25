import { describe, expect, it } from "vitest";
import { chooseRepresentative, comparisonFacts, liveResultHolders, shortProductName, threadTargetIds, turnResultGroups } from "./conversationResults";
import { pickLeaders, sortByPick } from "./researchCriteria";
import { threadOutcome, type ActionJob, type CurationThread } from "./thread";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";

const job = (targetId: string, count: number, facts?: ActionJob["facts"]): ActionJob => ({ jobId: `${targetId}:${count}`, actionId: "a", kind: "RESEARCH_ROUND", targetId, status: "SUCCEEDED", effects: [count > 0 ? { kind: "CANDIDATES_ADDED", targetId, count } : { kind: "NO_RESULTS", targetId }], facts });
const thread = (id: string, createdAt: string, jobs: ActionJob[], status = "SUCCEEDED"): CurationThread => ({ schemaVersion: "vitlane.curation-thread.v2", id, curationId: "c", mode: "AUTO", origin: "REQUEST", request: id, revision: 1, status, createdAt, updatedAt: createdAt,
  actions: [{ id: `${id}:research`, threadId: id, sequence: 0, type: "START_RESEARCH", status: "SUCCEEDED", decisionIds: [], jobs, effects: [], decisions: [], answers: [] }] });

describe("conversation results", () => {
  it("keeps each Target's live result in the latest response that added candidates for it", () => {
    const threads = [
      thread("first", "2026-09-21T06:10:00Z", [job("shoes", 16), job("pens", 10)]),
      thread("empty", "2026-09-21T06:30:00Z", [job("shoes", 0)]),
      thread("ink", "2026-09-21T06:21:00Z", [job("ink", 4)]),
      thread("running", "2026-09-21T06:40:00Z", [job("pens", 3)], "RUNNING"),
    ];
    const holders = liveResultHolders(threads, ["shoes", "pens", "ink", "notebook"]);
    expect(holders).toEqual({ shoes: "first", pens: "first", ink: "ink", notebook: undefined });
    // A Target whose every research came back empty still belongs to the response that researched it.
    expect(liveResultHolders([thread("only", "2026-09-21T06:10:00Z", [job("shoes", 0)])], ["shoes"])).toEqual({ shoes: "only" });
  });

  it("gives every turn the rows it worked on, from its first moment, and never moves them to a later turn (ADR-0089)", () => {
    const threads = [
      thread("first", "2026-09-21T06:10:00Z", [job("shoes", 16), job("pens", 10)]),
      thread("empty", "2026-09-21T06:30:00Z", [job("shoes", 0)]),
      thread("ink", "2026-09-21T06:21:00Z", [job("ink", 4)]),
      thread("running", "2026-09-21T06:40:00Z", [job("pens", 3)], "RUNNING"),
    ];
    // Turns in time order; a group researched again shows in both turns; the running turn already holds its group.
    expect(turnResultGroups(threads, ["shoes", "pens", "ink", "notebook"])).toEqual([
      { holderId: "first", targetIds: ["shoes", "pens"] }, { holderId: "ink", targetIds: ["ink"] }, { holderId: "empty", targetIds: ["shoes"] },
      { holderId: "running", targetIds: ["pens"] }, { holderId: undefined, targetIds: ["notebook"] },
    ]);
    // A request that has only planned its products holds them before any research runs, and a failed research keeps its row.
    const planned = thread("planned", "2026-09-21T07:00:00Z", [], "RUNNING");
    planned.actions[0] = { ...planned.actions[0], type: "INTENT_NEXT_STEP", effects: [{ kind: "TARGET_ADDED", targetId: "lamp" }, { kind: "TARGET_ADDED", targetId: "desk" }] };
    const failed = thread("failed", "2026-09-21T07:10:00Z", [{ ...job("lamp", 0), status: "FAILED" }], "FAILED");
    expect(threadTargetIds(planned)).toEqual(["lamp", "desk"]);
    expect(turnResultGroups([planned, failed], ["desk", "lamp"])).toEqual([
      { holderId: "planned", targetIds: ["desk", "lamp"] }, { holderId: "failed", targetIds: ["lamp"] },
    ]);
    // Researching one product group again names it on the action before its job exists.
    const again = thread("again", "2026-09-21T07:20:00Z", [], "RUNNING");
    again.actions[0] = { ...again.actions[0], type: "TARGET_RESEARCH_AGAIN", targetId: "desk", status: "PENDING" };
    expect(threadTargetIds(again)).toEqual(["desk"]);
    // The product groups can reach the page before the running request's record of them: a group made after
    // a running request started is that request's from the first moment, so it never shows at the end first.
    const planning = thread("planning", "2026-09-21T08:00:00Z", [], "RUNNING");
    const made = { cup: "2026-09-21T17:00:05+09:00", mug: "2026-09-21T07:59:00Z", old: "2026-09-20T00:00:00Z" };
    expect(turnResultGroups([planning], ["cup", "mug", "old"], made)).toEqual([
      { holderId: "planning", targetIds: ["cup"] }, { holderId: undefined, targetIds: ["mug", "old"] },
    ]);
    // Once the request has ended, a group it never recorded is nobody's.
    expect(turnResultGroups([{ ...planning, status: "SUCCEEDED" }], ["cup"], made)).toEqual([{ holderId: undefined, targetIds: ["cup"] }]);
  });

  it("adds up what a response looked at and keeps sources that returned nothing", () => {
    const facts = comparisonFacts(thread("first", "2026-09-21T06:10:00Z", [
      job("shoes", 16, { roundId: "r1", observed: 38, duplicates: 7, rejected: 1, admitted: 16, evaluated: 16, unevaluated: 0, sources: [{ source: "MUSINSA", status: "SUCCEEDED", candidateCount: 9 }, { source: "SSG", status: "EMPTY", candidateCount: 0 }] }),
      job("pens", 10, { roundId: "r2", observed: 21, duplicates: 4, rejected: 0, admitted: 10, evaluated: 9, unevaluated: 1, sources: [{ source: "ELEVENST", status: "SUCCEEDED", candidateCount: 6 }, { source: "MUSINSA", status: "SKIPPED", reasonCode: "CATALOG_API_RATE_LIMITED", candidateCount: 0 }] }),
    ]), ["shoes", "pens"]);
    expect(facts).toMatchObject({ observed: 59, compared: 25, duplicates: 11, unevaluated: 1 });
    expect(facts?.sources.map(s => `${s.source}:${s.candidateCount}:${s.status}`)).toEqual(["MUSINSA:9:SUCCEEDED", "ELEVENST:6:SUCCEEDED", "SSG:0:EMPTY"]);
    expect(comparisonFacts(thread("old", "2026-09-01T00:00:00Z", [job("shoes", 3)]), ["shoes"])).toBeUndefined();
  });

  it("shows the carted product, then what the reader last looked at, then the leader of the chosen sort", () => {
    const ordered = [{ candidateId: "leader" }, { candidateId: "second" }, { candidateId: "third" }];
    const none = () => false;
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true })).toEqual({ candidate: { candidateId: "leader" }, reason: "PICK" });
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: false })).toEqual({ candidate: { candidateId: "leader" }, reason: "SORT" });
    // Opening a product makes it the representative; a carted product still comes first.
    const viewed = { kind: "VIEWED" as const, candidateId: "third", at: "2026-09-21T07:00:00Z" };
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true, signal: viewed })).toEqual({ candidate: { candidateId: "third" }, reason: "VIEWED" });
    expect(chooseRepresentative({ ordered, inCart: id => id === "second", pickSort: true, signal: viewed })).toEqual({ candidate: { candidateId: "second" }, reason: "CART" });
    // Opening the leader itself keeps the leader's own reason: the row opens the representative's details all the time.
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true, signal: { ...viewed, candidateId: "leader" } })).toEqual({ candidate: { candidateId: "leader" }, reason: "PICK" });
    // Choosing a sort afterwards is the newer statement: its leader takes over.
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: false, signal: { kind: "SORT", at: "2026-09-21T07:05:00Z" } })?.reason).toBe("SORT");
    // A research that added candidates after the view resets to the leader, so the row agrees with the new reply.
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true, signal: viewed, researchedAt: "2026-09-21T07:30:00Z" })).toEqual({ candidate: { candidateId: "leader" }, reason: "PICK" });
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true, signal: viewed, researchedAt: "2026-09-21T06:30:00Z" })?.reason).toBe("VIEWED");
    // The Server writes its own offset: 15:30+09:00 is 06:30Z, before the 07:00Z view, although it sorts after it as text.
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true, signal: viewed, researchedAt: "2026-09-21T15:30:00.841046+09:00" })?.reason).toBe("VIEWED");
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true, signal: viewed, researchedAt: "2026-09-21T16:30:00+09:00" })?.reason).toBe("PICK");
    // A product that is hidden, disliked or unresolved is not in `ordered`, so it cannot stay the representative.
    expect(chooseRepresentative({ ordered, inCart: none, pickSort: true, signal: { ...viewed, candidateId: "hidden" } })?.candidate.candidateId).toBe("leader");
    expect(chooseRepresentative({ ordered: [], inCart: none, pickSort: true })).toBeUndefined();
  });

  it("reads a product name as a short phrase inside a sentence", () => {
    expect(shortProductName("[11번가] [정품] 라미 사파리 만년필")).toBe("라미 사파리 만년필");
    expect(shortProductName("삼성전자 갤럭시 버즈3 프로 노이즈 리덕션 무선 블루투스이어폰")).toBe("삼성전자 갤럭시 버즈3 프로 노이즈…");
    expect(shortProductName("(무료배송) Pilot Custom 74")).toBe("Pilot Custom 74");
    expect(shortProductName("[정품]")).toBe("[정품]");
    expect(shortProductName("Supercalifragilisticexpialidocious pen")).toBe("Supercalifragilisticex…");
  });

  it("makes Vitlane Pick the best score that fits the budget", () => {
    const product = (candidateId: string, totalScore?: number): LiveCatalogProduct => ({ candidateId, title: candidateId, description: "", currency: "KRW", categories: [], features: [], specifications: [],
      axisAssessment: totalScore === undefined ? undefined : { totalScore } as LiveCatalogProduct["axisAssessment"] });
    const cohort = [product("unscored"), product("over", 91), product("fit", 90), product("cheap", 81)];
    const fits = (p: LiveCatalogProduct) => p.candidateId === "over" ? false : p.candidateId === "unscored" ? undefined : true;
    expect([...pickLeaders(cohort, fits)]).toEqual(["fit"]);
    expect(sortByPick(cohort, cohort, fits).map(p => p.candidateId)).toEqual(["fit", "over", "cheap", "unscored"]);
    expect([...pickLeaders(cohort, () => undefined)]).toEqual(["over"]);
    expect([...pickLeaders(cohort, () => false)]).toEqual(["over"]);
  });

  it("reads a question Thread as answered, with or without the reply", () => {
    const answered: CurationThread = { ...thread("q", "2026-09-21T06:16:00Z", []), actions: [
      { id: "q:start", threadId: "q", sequence: 0, type: "AUTO_START", status: "SUCCEEDED", decisionIds: [], jobs: [], effects: [], decisions: [], answers: [] },
      { id: "q:reply", threadId: "q", sequence: 1, type: "RESPONSE", instruction: "ANSWER", status: "SUCCEEDED", reasonCode: "RESPONSE_UNAVAILABLE", decisionIds: [], jobs: [], effects: [], decisions: [], answers: [] }] };
    expect(threadOutcome(answered)).toBe("ANSWERED");
  });
});

it("does not move or merge research results when a combination reply is appended", () => {
 const pen = thread("pen-research", "2026-09-21T06:10:00Z", [job("pen",2)]);
 const ink = thread("ink-research", "2026-09-21T06:15:00Z", [job("ink",3)]);
 const reply = thread("combination", "2026-09-21T06:20:00Z", []);
 reply.actions = [{...reply.actions[0],type:"RESPONSE",response:{
  schemaVersion:"vitlane.thread-response.v2",kind:"COMMENT",body:"Use these together.",locale:"en-US",references:[],createdAt:reply.createdAt,
  combination:{criteriaVersions:{pen:1,ink:1}} as unknown as NonNullable<import("./thread").ThreadResponse["combination"]>,
 }}];
 const before = liveResultHolders([pen,ink], ["pen","ink"]);
 const after = liveResultHolders([pen,ink,reply], ["pen","ink"]);
 expect(after).toEqual(before);
 expect(turnResultGroups([pen,ink,reply], ["pen","ink"])).toEqual([
  {holderId:"pen-research",targetIds:["pen"]},{holderId:"ink-research",targetIds:["ink"]},
 ]);
});


it("lets a viewed replacement remain representative after the initial combination was carted",()=>{
 const a={candidateId:"a"},c={candidateId:"c"};
 expect(chooseRepresentative({ordered:[a,c],inCart:id=>id==="a",pickSort:false,combination:true,signal:{kind:"VIEWED",candidateId:"c",at:"2026-09-23T00:00:00Z"}})?.candidate).toBe(c);
 expect(chooseRepresentative({ordered:[c,a],inCart:id=>id==="a",pickSort:false,combination:true,signal:{kind:"SORT",at:"2026-09-23T00:00:00Z"}})?.candidate).toBe(c);
});
