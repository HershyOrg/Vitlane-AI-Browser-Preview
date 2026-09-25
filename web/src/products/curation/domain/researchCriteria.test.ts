import vector from "../../../../../shared/openapi/fixtures/curation-step4.v1.json";
import { describe, expect, it } from "vitest";
import { axisValue, comparativeStrength, sortResearchProducts, sortValue, type AxisAssessment, type ResearchCriteria } from "./researchCriteria";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
const axis = (id: string, importance = 3) => ({ axisId: id, label: id, definition: id, importance, usesPrice: false, usesVisualEvidence: false, origin: "REQUEST" as const });
const criteria: ResearchCriteria = { schemaVersion: "vitlane.target-criteria.v1", version: 1, subject: { label: "pen", productType: "fountain pen" }, axes: [axis("a"), axis("b")], exclusions: [] };
function candidate(id: string, total?: number, scores: [string, number][] = []): LiveCatalogProduct {
 return { candidateId: id, title: id, description: "", currency: "USD", categories: [], features: [], specifications: [], ...(total === undefined ? {} : { axisAssessment: { observationHash: "hash", source: "SHOPIFY", schemaVersion: "vitlane.axis-assessment.v1", criteria: structuredClone(criteria), weights: [50, 50], scores: scores.map(([axisId, scorePercent]) => ({ axisId, scorePercent, basis: "UNKNOWN", explanation: "unknown", factIds: [] })), totalScore: total, totalBasisPoints: total * 100, contentLocale: "ko-KR", roundId: "r", modelKey: "m", createdAt: "2026-09-12T00:00:00Z" } as AxisAssessment }) };
}
describe("frozen research comparisons", () => {
 it("matches the shared missing-axis and frozen Pick contract", () => {
  const products=vector.sorting.candidates.map(c=>candidate(c.id,c.total,c.axisScore===undefined?[]:[[vector.sorting.axisId,c.axisScore]]));
  expect(sortResearchProducts(products,"PICK",()=>undefined).map(c=>c.candidateId)).toEqual(vector.sorting.pick);
  expect(sortResearchProducts(products,`AXIS:${vector.sorting.axisId}`,()=>undefined).map(c=>c.candidateId)).toEqual(vector.sorting.axis);
 });
 it("preserves 97 after adding d and sorts unassessed behind a real zero", () => {
  const old = candidate("old", 97, [["a", 95]]), zero = candidate("new", 30, [["d", 0]]), legacy = candidate("legacy"); const before = JSON.stringify(old);
  const next = { ...criteria, axes: [...criteria.axes, axis("d")], version: 2 };
  expect(next.axes.length).toBe(3); expect(axisValue(old, "d")).toBeUndefined();
  expect(sortResearchProducts([old, zero, legacy], "AXIS:d", () => undefined).map(x => x.candidateId)).toEqual(["new", "old", "legacy"]);
  expect(sortResearchProducts([legacy, zero, old], "PICK", () => undefined).map(x => x.candidateId)).toEqual(["old", "new", "legacy"]);
  expect(JSON.stringify(old)).toBe(before);
 });
 it("compares the same axis across candidates, not the product's largest raw score", () => {
  const p = candidate("p", 85, [["a", 90], ["b", 80]]), peer = candidate("peer", 80, [["a", 99], ["b", 50]]);
  const result = comparativeStrength(p, [p, peer], criteria)!; expect(result.axis.axisId).toBe("b"); expect(result.rank).toBe(1); expect(result.count).toBe(2); expect(result.percentile).toBe(100);
  expect(comparativeStrength(p, [p], criteria)).toBeUndefined();
 });
 it("uses a stable cohort and puts unknown prices last without mutating input", () => {
  const cohort = [candidate("a", 97), candidate("b", 97), candidate("c", 60)]; const prices = new Map([["a", 100], ["b", 100]]); const get = (p: LiveCatalogProduct) => prices.get(p.candidateId);
  expect(sortResearchProducts([cohort[2], cohort[1], cohort[0]], "PICK", get, cohort).map(p => p.candidateId)).toEqual(["a", "b", "c"]);
  expect(sortResearchProducts(cohort, "PRICE_ASC", get).map(p => p.candidateId)).toEqual(["a", "b", "c"]);
  expect(cohort.map(p => p.candidateId)).toEqual(["a", "b", "c"]);
 });
 it("sorts the highest price first and still puts unknown prices last", () => {
  const cohort = [candidate("unknown", 90), candidate("cheap", 70), candidate("pricey", 50), candidate("tied", 60)]; const prices = new Map([["cheap", 100], ["pricey", 300], ["tied", 300]]); const get = (p: LiveCatalogProduct) => prices.get(p.candidateId);
  expect(sortResearchProducts(cohort, "PRICE_DESC", get).map(p => p.candidateId)).toEqual(["pricey", "tied", "cheap", "unknown"]);
  expect(sortResearchProducts(cohort, "PRICE_ASC", get).map(p => p.candidateId)).toEqual(["cheap", "pricey", "tied", "unknown"]);
  expect(sortValue(cohort[2], "PRICE_DESC", get)).toBe(300);
  expect(sortValue(cohort[2], "PICK", get)).toBe(50);
 });
});
