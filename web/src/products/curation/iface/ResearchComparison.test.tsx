// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import type { AxisAssessment, ResearchCriteria } from "../domain/researchCriteria";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
import { shopifyPresentation, amazonPresentation } from "../infra/candidatePresentation";
import { CurationCandidateCard } from "./CurationCandidateCard";
import { CandidateDetailDialog } from "./CandidateDetailDialog";
import { CriteriaEditor, TargetComparison } from "./ResearchComparison";
const criteria: ResearchCriteria = { schemaVersion: "vitlane.target-criteria.v1", version: 1, subject: { label: "펜", productType: "pen" }, exclusions: [], axes: ["Writing", "Design"].map((label, i) => ({ axisId: `axis-${i}`, label, definition: label, importance: 3, usesPrice: false, usesVisualEvidence: false, origin: "REQUEST" })) };
const assessment: AxisAssessment = { schemaVersion: "vitlane.axis-assessment.v1", source: "SHOPIFY", observationHash: "hash", criteria, weights: [50,50], scores: [90,80].map((scorePercent, i) => ({axisId: `axis-${i}`, scorePercent, basis: "UNKNOWN", explanation: i ? "디자인은 원문에서 확인하지 못했습니다." : "필기감은 직접 확인하지 못했습니다.", factIds: []})), totalScore: 85, totalBasisPoints: 8500, contentLocale: "ko-KR", roundId: "round", modelKey: "fixture", createdAt: "2026-09-12T00:00:00Z" };
const product: LiveCatalogProduct = { candidateId: "pen", title: "Pen", description: "", intentPoint: "Saved recommendation", currency: "USD", categories: [], features: [], specifications: [], axisAssessment: assessment };
afterEach(() => { window.localStorage.clear(); vi.unstubAllGlobals(); });
it("projects each saved importance level onto its axis tag without changing criteria", async () => {
  const value: ResearchCriteria = { ...criteria, axes: [1,2,3,4,5].map(importance => ({ ...criteria.axes[0], axisId: `axis-${importance}`, label: `Axis ${importance}`, importance })) };
  const before = JSON.stringify(value), el = document.createElement("div"), root = createRoot(el);
  try {
    await act(async () => root.render(<LocaleProvider><CriteriaEditor criteria={value} curationId="curation" targetId="target" version={1} onSave={() => {}} /></LocaleProvider>));
    expect([...el.querySelectorAll("[data-importance]")].map(e=>e.getAttribute("data-importance"))).toEqual(["1","2","3","4","5"]);
    expect(el.querySelectorAll(".research-axis-trigger:not([data-importance])")).toHaveLength(1);
    expect(JSON.stringify(value)).toBe(before);
  } finally { await act(async () => root.unmount()); }
});
for (const locale of ["ko-KR", "en-US"] as const) it(`${locale}: comparative card and modal retain the saved assessment outside the target context`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`;
  const saved = JSON.stringify(assessment);
  const presentation = shopifyPresentation({ candidateId: "pen", title: "Pen", previewPriceMinor: 2000, currency: "USD", features: [], specifications: [], intentPoint: "Saved recommendation", axisAssessment: assessment });
  expect(presentation.axisAssessment).toBe(assessment);
  expect(amazonPresentation(product).axisAssessment).toBe(assessment);
  const peer = { ...product, candidateId: "peer", axisAssessment: { ...assessment, scores: assessment.scores.map((s,i)=>({...s,scorePercent:i?50:99})) } };
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  const close=vi.fn();
  try {
    await act(async()=>root.render(<LocaleProvider><TargetComparison products={[product,peer]} cohort={[product,peer]} criteria={criteria}>{()=> <CurationCandidateCard candidate={presentation} />}</TargetComparison></LocaleProvider>));
    expect(el.querySelector(".candidate-comparison__score")?.textContent).toBe("85/100");
    expect(el.querySelector(".vt-candidate-card__price-row .candidate-comparison__score")).not.toBeNull();
    expect(el.querySelector(".candidate-comparison .candidate-comparison__score")).toBeNull();
    expect(el.querySelector(".candidate-comparison__axis")?.textContent).toBe("#Design");
    expect(el.querySelector(".vt-candidate-card__evidence")?.textContent).toContain(assessment.scores[1].explanation);
    expect(el.querySelector(".candidate-axis-details")).toBeNull();
    expect(el.querySelectorAll("details, select, input")).toHaveLength(0);
    expect(el.querySelector(".candidate-ranking-badge")?.textContent).not.toMatch(/공동|tied/i);
    // The modal is a sibling of TargetComparison in the real Shopify workspace.
    await act(async()=>root.render(<LocaleProvider><CandidateDetailDialog candidate={presentation} options={[]} interaction={{pinned:false,sentiment:"NONE"}} onReaction={()=>{}} onSelect={()=>{}} onSave={()=>{}} onClose={close} purchase={{kind:"CART",label:"Cart",disabled:true,onAction:()=>{}}} /></LocaleProvider>));
    expect(document.querySelectorAll(".candidate-detail meter")).toHaveLength(0);
    expect([...document.querySelectorAll(".candidate-axis-details__score")].map(e=>e.textContent)).toEqual(["90/100","80/100"]);
    expect(document.querySelector(".candidate-axis-details")?.textContent).toContain(locale === "ko-KR" ? "조사 당시 평가" : "Assessment at discovery");
    expect(document.querySelector(".candidate-axis-details")?.textContent).toContain(assessment.scores[1].explanation);
    const sections = [...document.querySelectorAll<HTMLButtonElement>(".candidate-detail__section .vt-disclosure__trigger")];
    expect(sections).toHaveLength(5);
    expect(sections.map(e=>e.getAttribute("aria-expanded"))).toEqual(["true","true","true","true","true"]);
    await act(async()=>sections[2].click());
    expect(sections[2].getAttribute("aria-expanded")).toBe("false");
    await act(async()=>sections[2].click());
    expect(sections[2].getAttribute("aria-expanded")).toBe("true");
    expect(document.querySelector(".candidate-detail")?.textContent).not.toMatch(/가중치|Weight|These scores include|추정을 포함|옵션 다시 불러오기|Reload options/);
    expect(JSON.stringify(assessment)).toBe(saved);
  } finally { await act(async()=>root.unmount());el.remove(); }
});
for (const locale of ["ko-KR", "en-US"] as const) it(`${locale}: sort trigger leads with the sort icon and highest price ranks the most expensive first`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`;
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const ko = locale === "ko-KR";
  const priced = (candidateId: string, priceMinimumMinor?: number): LiveCatalogProduct => ({ ...product, candidateId, title: candidateId, currency: "KRW", priceMinimumMinor });
  const cohort = [priced("cheap", 10000), priced("unknown"), priced("pricey", 30000)];
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  try {
    await act(async()=>root.render(<LocaleProvider><TargetComparison products={cohort} cohort={cohort} criteria={criteria}>{sorted => sorted.map(p => <CurationCandidateCard key={p.candidateId} candidate={amazonPresentation(p)} />)}</TargetComparison></LocaleProvider>));
    const trigger = el.querySelector<HTMLButtonElement>(".research-sort__trigger")!;
    expect(trigger.firstElementChild?.matches("svg.lucide-arrow-up-down[aria-hidden='true']")).toBe(true);
    expect(trigger.textContent).toBe(ko ? "정렬 · Vitlane Pick" : "Sort · Vitlane Pick");
    await act(async()=>{ trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); });
    const items = [...document.querySelectorAll<HTMLElement>("[role='menuitemradio']")];
    expect(items.map(e=>e.textContent)).toEqual(ko ? ["Vitlane Pick", "낮은 가격순", "높은 가격순", "Writing", "Design"] : ["Vitlane Pick", "Lowest price", "Highest price", "Writing", "Design"]);
    await act(async()=>items[2].click());
    expect(trigger.textContent).toBe(ko ? "정렬 · 높은 가격순" : "Sort · Highest price");
    expect([...el.querySelectorAll(".vt-candidate-card__title")].map(e=>e.textContent)).toEqual(["pricey", "cheap", "unknown"]);
    expect([...el.querySelectorAll(".candidate-ranking-badge")].map(e=>e.textContent)).toEqual([ko ? "최고가" : "Highest price"]);
  } finally { await act(async()=>root.unmount());el.remove(); }
});
