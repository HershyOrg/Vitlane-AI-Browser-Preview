// @vitest-environment jsdom
import { act, useEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { createPortal } from "react-dom";
import { afterEach, expect, it, vi } from "vitest";
import { CurationThreadProgress, CurationThreadReport } from "./CurationThread";
import { ConversationResultsProvider, useConversationResults } from "../app/useConversationResults";
import { useCurationThreads } from "../app/useThreads";
import type { CurationActionExecution, CurationThread, ThreadResponse } from "../domain/thread";

vi.mock("../app/useThreads", () => ({ useCurationThreads: vi.fn() }));
vi.mock("../infra/curationApi", () => ({ retryIntelligenceJob: vi.fn() }));
let locale = "ko-KR";
const l = (en: string, ko: string, values?: Record<string, string | number>) => Object.entries(values ?? {}).reduce((s, [k, v]) => s.replaceAll(`{${k}}`, String(v)), locale === "ko-KR" ? ko : en);
vi.mock("../../../shared/i18n", () => ({ useLocale: () => ({ locale, l }), localizeFixedCopy: (_en: string, ko: string) => ko, invariantContent: (v: string) => v }));

let root: Root;
afterEach(async () => { await act(async () => root?.unmount()); document.body.innerHTML = ""; vi.clearAllMocks(); locale = "ko-KR"; });

const base: CurationActionExecution = { id: "auto", threadId: "request", sequence: 0, type: "AUTO_START", status: "SUCCEEDED", jobs: [], effects: [], decisions: [], decisionIds: [], answers: [] };
const research: CurationActionExecution = { ...base, id: "research", type: "START_RESEARCH", sequence: 1, jobs: [{ jobId: "j1", actionId: "research", kind: "RESEARCH_ROUND", targetId: "pen", status: "SUCCEEDED", effects: [{ kind: "CANDIDATES_ADDED", targetId: "pen", count: 6 }] }] };
const comment: ThreadResponse = { schemaVersion: "vitlane.thread-response.v1", kind: "COMMENT", body: "필기감은 [[c1]]가 가장 고르게 좋아요. 가볍게 쓰려면 [[c2]]도 볼 만해요.", locale: "ko-KR", references: [{ ref: "c1", candidateId: "cand-1", targetId: "pen", title: "파이롯트 커스텀 74" }, { ref: "c2", candidateId: "cand-2", targetId: "pen", title: "라미 사파리" }], createdAt: "2026-09-21T00:00:05Z" };
function thread(actions: CurationActionExecution[], status: CurationThread["status"] = "SUCCEEDED"): CurationThread {
  return { schemaVersion: "vitlane.curation-thread.v2", id: "request", curationId: "curation", mode: "AUTO", origin: "REQUEST", request: "만년필 찾아줘", revision: 3, status, actions, targetLabels: { pen: "만년필" }, createdAt: "2026-09-21T00:00:00Z", updatedAt: "2026-09-21T00:00:06Z" };
}
function controller(threads: CurationThread[], active?: CurationThread) {
  return { threads, active, sending: false, busy: Boolean(active), reload: vi.fn(), cancel: vi.fn(), sheetOpen: () => undefined, rememberSheet: () => undefined } as unknown as ReturnType<typeof useCurationThreads>;
}
function BindOpener({ open }: { open: (candidateId: string, targetId: string) => void }) {
  const results = useConversationResults();
  useEffect(() => results?.bindOpenCandidate(open), [results, open]);
  return null;
}
async function mount(node: React.ReactNode) { const el = document.createElement("div"); document.body.append(el); root = createRoot(el); await act(async () => root.render(node)); }
function PortaledProductImage({ threadId }: { threadId: string }) {
  const host = useConversationResults()?.hosts[threadId];
  return host ? createPortal(<img alt="대표 상품" src="/product.jpg" />, host) : null;
}

const buttonNamed = (text: string) => [...document.querySelectorAll("button")].find(b => b.textContent?.trim() === text);

it("shows the written reply as body text and opens a product from its name", async () => {
  const t = thread([base, research, { ...base, id: "reply", type: "RESPONSE", sequence: 2, instruction: "COMMENT", response: comment, jobs: [{ jobId: "j2", actionId: "reply", kind: "ACTION_INTERPRETATION", status: "SUCCEEDED", effects: [] }] }]);
  vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
  const open = vi.fn();
  await mount(<ConversationResultsProvider><BindOpener open={open} /><CurationThreadReport thread={t} /></ConversationResultsProvider>);

  const reply = document.querySelector('[data-response-kind="COMMENT"]')!;
  // The model never spells a product name: the Server's saved title replaces each reference.
  expect(reply.textContent).toBe("필기감은 파이롯트 커스텀 74가 가장 고르게 좋아요. 가볍게 쓰려면 라미 사파리도 볼 만해요.");
  expect(reply.textContent).not.toContain("[[");
  // Body text carries no sender line: no logo, no name, no time. Only assistive technology hears who speaks.
  const response = document.querySelector("article.curation-response")!;
  expect(response.querySelector("header, svg, time")).toBeNull();
  expect(response.querySelector(".vt-visually-hidden")?.textContent).toBe("Vitlane");
  // The reader's bubble is the words only; the time is a tooltip.
  const bubble = document.querySelector(".curation-thread__request")!;
  expect(bubble.querySelector("header, time")).toBeNull();
  expect(bubble.querySelector("p")?.textContent).toBe("만년필 찾아줘");
  expect(bubble.getAttribute("title")).toBeTruthy();
  // With a written reply the fixed sentences step aside entirely.
  expect(response.querySelector(".curation-thread-report__headline")).toBeNull();
  // The rows below already say how many were found, so the fixed sentence steps aside.
  expect(document.querySelector(".curation-thread-report")!.textContent).not.toContain("후보 6개를 찾았어요");
  expect(document.querySelector(".is-vitlane")).toBeNull();
  expect(document.querySelector(".curation-response__results")).not.toBeNull();

  await act(async () => buttonNamed("라미 사파리")!.click());
  expect(open).toHaveBeenCalledWith("cand-2", "pen");

  // The structured record stays one click away, and the reply step reads as a step, not as a model job.
  await act(async () => buttonNamed("처리 내역")!.click());
  expect(document.querySelectorAll("[data-action-id]")).toHaveLength(3);
  expect(document.querySelector('[data-action-id="reply"]')!.textContent).toContain("응답 작성");
  // The time left the conversation; the record keeps it.
  expect(document.querySelector(".curation-response__record time")?.getAttribute("datetime")).toBe("2026-09-21T00:00:06Z");
});

it("reads a long listing title as a short name inside the sentence and keeps the full title on the link", async () => {
  const long: ThreadResponse = { ...comment, body: "[[c1]]가 소음 차단에서 앞서요.", references: [{ ref: "c1", candidateId: "cand-9", targetId: "buds", title: "[쿠팡] [정품] 삼성전자 갤럭시 버즈3 프로 노이즈 리덕션 무선 블루투스이어폰" }] };
  const t = thread([base, research, { ...base, id: "reply", type: "RESPONSE", sequence: 2, instruction: "COMMENT", response: long }]);
  vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
  const open = vi.fn();
  await mount(<ConversationResultsProvider><BindOpener open={open} /><CurationThreadReport thread={t} /></ConversationResultsProvider>);
  const link = document.querySelector<HTMLButtonElement>(".curation-response__ref")!;
  expect(link.textContent).toBe("삼성전자 갤럭시 버즈3 프로 노이즈…");
  expect(link.getAttribute("aria-label")).toBe("[쿠팡] [정품] 삼성전자 갤럭시 버즈3 프로 노이즈 리덕션 무선 블루투스이어폰");
  expect(link.getAttribute("aria-haspopup")).toBe("dialog");
  await act(async () => link.click());
  expect(open).toHaveBeenCalledWith("cand-9", "buds");
});

function PublishTitles({ titles }: { titles: Record<string, string> }) {
  const results = useConversationResults();
  const publish = results?.publishTitles;
  useEffect(() => publish?.(titles), [publish, titles]);
  return null;
}

it("names a product the Server could not title by the name the page has read, and never leaves the link empty", async () => {
  // Vitlane saves no display facts of a Shopify product, so its reference arrives without a title.
  const untitled: ThreadResponse = { ...comment, body: "[[c1]]가 가장 고르게 좋아요.", references: [{ ref: "c1", candidateId: "phase8-1", targetId: "chair", title: "" }] };
  const t = thread([base, research, { ...base, id: "reply", type: "RESPONSE", sequence: 2, instruction: "COMMENT", response: untitled }]);
  vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
  await mount(<ConversationResultsProvider><CurationThreadReport thread={t} /></ConversationResultsProvider>);
  expect(document.querySelector(".curation-response__ref")!.textContent).toBe("이 상품");
  await act(async () => root.unmount());
  const titles = { "phase8-1": "Cinder folding camp chair" };
  await mount(<ConversationResultsProvider><PublishTitles titles={titles} /><CurationThreadReport thread={t} /></ConversationResultsProvider>);
  const link = document.querySelector<HTMLButtonElement>(".curation-response__ref")!;
  expect(link.textContent).toBe("Cinder folding camp…");
  expect(link.getAttribute("aria-label")).toBe("Cinder folding camp chair");
});

it("puts what the research looked at under the response on the left, and the record right under it", async () => {
  const facts = { roundId: "r1", observed: 24, duplicates: 3, rejected: 1, admitted: 12, evaluated: 12, unevaluated: 0, sources: [{ source: "ELEVENST", status: "SUCCEEDED", candidateCount: 9 }, { source: "COUPANG", status: "SUCCEEDED", candidateCount: 3 }, { source: "AMAZON", status: "UNSUPPORTED", candidateCount: 0 }] };
  const researched: CurationActionExecution = { ...research, jobs: [{ ...research.jobs[0], facts }] };
  const t = thread([base, researched, { ...base, id: "reply", type: "RESPONSE", sequence: 2, instruction: "COMMENT", response: comment }]);
  vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
  await mount(<CurationThreadReport thread={t} />);
  const foot = document.querySelector(".curation-response__foot")!;
  expect(foot.querySelector(".curation-result__facts")?.textContent).toBe("11번가 · 쿠팡 — 24개 확인 · 12개 비교");
  expect(foot.querySelector(".curation-response__foot-actions")?.textContent).toBe("처리 내역");
  // Reading order is the visual order: the numbers first, the record under them, both at the left edge.
  expect([...foot.children].map(child => child.className)).toEqual(["vt-button vt-button--quiet curation-result__facts", "curation-response__foot-actions"].map(name => expect.stringContaining(name.split(" ").at(-1)!)));
  // A request without a research (a question, a settings change) has nothing to count.
  await act(async () => root.unmount()); document.body.innerHTML = "";
  const plainThread = thread([base]);
  vi.mocked(useCurationThreads).mockReturnValue(controller([plainThread]));
  await mount(<CurationThreadReport thread={plainThread} />);
  expect(document.querySelector(".curation-result__facts")).toBeNull();
});

it("answers a question without work and tells where to check price and stock", async () => {
  const answer: ThreadResponse = { ...comment, kind: "ANSWER", body: "두 상품의 차이는 닙 굵기예요. [[c1]]는 $150.00이에요." };
  const t = thread([{ ...base, id: "reply", type: "RESPONSE", instruction: "ANSWER", response: answer }]);
  t.request = "둘 중 뭐가 더 나아?";
  vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
  await mount(<CurationThreadReport thread={t} />);

  const report = document.querySelector(".curation-thread-report")!;
  expect(report.getAttribute("data-thread-outcome")).toBe("ANSWERED");
  expect(document.querySelector('[data-response-kind="ANSWER"]')!.textContent).toContain("파이롯트 커스텀 74는 $150.00이에요.");
  expect(report.textContent).toContain("가격은 조회된 판매 페이지 기준이며, 색상·사이즈별 재고와 할인은 구매 시 확인이 필요해요.");
  expect(buttonNamed("다시 시도")).toBeUndefined();
  expect(report.querySelector(".vt-chip")).toBeNull();
});

it("keeps the fixed sentences when the reply could not be written", async () => {
  const t = thread([base, research, { ...base, id: "reply", type: "RESPONSE", sequence: 2, instruction: "COMMENT", reasonCode: "RESPONSE_UNAVAILABLE", jobs: [{ jobId: "j2", actionId: "reply", kind: "ACTION_INTERPRETATION", status: "FAILED", reasonCode: "THREAD_RESPONSE_REFERENCE_UNKNOWN", effects: [] }] }]);
  vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
  await mount(<CurationThreadReport thread={t} />);

  const report = document.querySelector(".curation-thread-report")!;
  // Losing the reply never changes the outcome of the request.
  expect(report.getAttribute("data-thread-outcome")).toBe("SUCCEEDED");
  expect(report.textContent).toContain("만년필 후보 6개를 찾았어요.");
  expect(report.querySelector(".curation-thread-report__headline")!.compareDocumentPosition(report.querySelector(".curation-response__results")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(document.querySelector("[data-response-kind]")).toBeNull();
  expect(buttonNamed("다시 시도")).toBeUndefined();
  await act(async () => buttonNamed("처리 내역")!.click());
  expect(document.querySelector('[data-action-id="reply"]')!.textContent).toContain("응답 글 없이 마침");
});

it.each([["ko-KR", "응답 작성 중"], ["en-US", "Writing response"]])("reports the reply step in the bar while it is being written (%s)", async (language, label) => {
  locale = language;
  const t = thread([base, research, { ...base, id: "reply", type: "RESPONSE", sequence: 2, status: "RUNNING", instruction: "COMMENT" }], "RUNNING");
  vi.mocked(useCurationThreads).mockReturnValue(controller([t], t));
  await mount(<CurationThreadProgress />);
  expect(document.querySelector('.curation-thread__toggle [role="status"]')!.textContent).toContain(label);
});

it.each(["ko-KR", "en-US"])("keeps a running request in its turn and places the completed explanation above its product image (%s)", async language => {
  locale = language;
  const plan: CurationActionExecution = { ...base, id: "plan", type: "INTENT_NEXT_STEP", sequence: 1, effects: [
    { kind: "TARGET_ADDED", targetId: "pen", targetLabel: "만년필" }, { kind: "TARGET_ADDED", targetId: "ink", targetLabel: "잉크" },
    { kind: "BUDGET_CHANGED", after: { enabled: true, currency: "KRW", allocations: [{ targetId: "pen", amount: "150000" }, { targetId: "ink", amount: null }] } },
  ] as CurationActionExecution["effects"] };
  const t = thread([base, plan, { ...research, status: "RUNNING", jobs: [] }], "RUNNING");
  vi.mocked(useCurationThreads).mockReturnValue(controller([t], t));
  await mount(<ConversationResultsProvider><CurationThreadReport thread={t} /><PortaledProductImage threadId={t.id} /></ConversationResultsProvider>);

  const turn = document.querySelector("[data-thread-outcome=\"ACTIVE\"]")!;
  const reply = turn.querySelector("[data-turn-state=\"active\"]")!;
  expect(turn.querySelector(".curation-thread__request")!.compareDocumentPosition(reply) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(reply.querySelector(".curation-response__lead")!.textContent).toBe(language === "ko-KR" ? "2가지로 나눠 찾아볼게요." : "I’ll look for these as 2 products.");
  // Research can show a product while the written reply is still pending.
  expect(reply.querySelector(".curation-response__lead + .curation-response__results img")?.getAttribute("alt")).toBe("대표 상품");
  expect(reply.querySelector(".curation-response__foot")).toBeNull();
  expect(reply.getAttribute("role")).toBeNull();

  // The same turn ends in place: the finished explanation precedes the product.
  const done = thread([base, { ...plan, status: "SUCCEEDED" }, { ...research, status: "SUCCEEDED", jobs: [] }, { ...base, id: "reply", type: "RESPONSE", sequence: 3, instruction: "COMMENT", response: comment }], "SUCCEEDED");
  vi.mocked(useCurationThreads).mockReturnValue(controller([done]));
  await mount(<ConversationResultsProvider><CurationThreadReport thread={done} /><PortaledProductImage threadId={done.id} /></ConversationResultsProvider>);
  const finished = document.querySelector("[data-turn-state=\"done\"]")!;
  expect(finished.querySelector(".curation-response__lead")!.textContent).toBe(language === "ko-KR" ? "2가지로 나눠 찾아볼게요." : "I’ll look for these as 2 products.");
  const explanation = finished.querySelector("[data-response-kind=COMMENT]")!;
  const product = finished.querySelector(".curation-response__results img")!;
  expect(explanation.compareDocumentPosition(product) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(product.compareDocumentPosition(finished.querySelector(".curation-response__foot")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

it.each(["ko-KR", "en-US"])("discloses listing prices under both reply kinds in %s", async language => {
  locale = language;
  await mount(<div />);
  for (const kind of ["COMMENT", "ANSWER"] as const) {
    const response = { ...comment, kind, body: "[[c1]]: $150.00." };
    const t = thread([{ ...base, id: "reply", type: "RESPONSE", instruction: kind, response }]);
    vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
    await act(async () => root.render(<CurationThreadReport thread={t} />));
    const captions = document.querySelectorAll(".curation-response__caption");
    expect(captions).toHaveLength(1);
    expect(captions[0].textContent).toBe(language === "ko-KR"
      ? "가격은 조회된 판매 페이지 기준이며, 색상·사이즈별 재고와 할인은 구매 시 확인이 필요해요."
      : "Prices reflect the listings checked. Confirm availability by color and size, and discounts, when purchasing.");
  }
});

it("keeps combination advice inside the existing reply text without a separate layout", async () => {
 const reply: ThreadResponse = { ...comment, schemaVersion: "vitlane.thread-response.v2", combination: {
   reasons: ["두 후보의 색이 어울려요."], tips: [{label:"활용", body:"[[c2]]를 편하게 활용하세요."}],
   cautions: ["옵션은 구매 전에 확인하세요."], budgetAdvice: "남은 예산은 쓰지 않아도 돼요.",
 } as NonNullable<ThreadResponse["combination"]> };
 const t = thread([{...base, id:"reply",type:"RESPONSE",response:reply}]);
 vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
 await mount(<ConversationResultsProvider><CurationThreadReport thread={t}/></ConversationResultsProvider>);
 const text = document.querySelector(".curation-response__text")!;
 expect(text.textContent).toContain("두 후보의 색이 어울려요.");
 expect(text.textContent).toContain("남은 예산은 쓰지 않아도 돼요.");
 expect(text.querySelector("button")?.className).toContain("curation-response__ref");
 expect(document.querySelector(".combination-response, .combination-review, .combination-response__money")).toBeNull();
 expect(buttonNamed("조합 확인")).toBeUndefined();
 expect(buttonNamed("이렇게 활용하면 좋아요")).toBeUndefined();
});

it.each([["ko-KR", "조합 추천"], ["en-US", "Recommend a combination"]])("names a manual combination request in %s", async (language, label) => {
 locale = language;
 const t = {...thread([{...base, id:"reply", type:"RESPONSE", instruction:"COMBINATION", response:comment}]), request:""};
 vi.mocked(useCurationThreads).mockReturnValue(controller([t]));
 await mount(<CurationThreadReport thread={t}/>);
 expect(document.querySelector(".curation-thread__request p")?.textContent).toBe(label);
});
