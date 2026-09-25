import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
export type ResearchAxis = { axisId: string; label: string; definition: string; importance: number; usesPrice: boolean; usesVisualEvidence: boolean; origin: "REQUEST" | "FEEDBACK" | "USER_EDIT" };
export type ResearchCriteria = { schemaVersion: "vitlane.target-criteria.v1"; version: number; subject: { label: string; productType: string }; axes: ResearchAxis[]; exclusions: string[] };
export type AxisAssessment = { observationHash: string; source: string; schemaVersion: "vitlane.axis-assessment.v1"; criteria: ResearchCriteria; weights: number[]; scores: Array<{ axisId: string; scorePercent: number; basis: "PROVIDED" | "INFERRED" | "UNKNOWN"; explanation: string; factIds: string[] }>; totalBasisPoints: number; totalScore: number; contentLocale: string; roundId: string; modelKey: string; createdAt: string };
export type ResearchSort = "PICK" | "COMBINATION" | "PRICE_ASC" | "PRICE_DESC" | `AXIS:${string}`;
export function axisValue(p: LiveCatalogProduct, id: string) { return p.axisAssessment?.scores.find(s => s.axisId === id)?.scorePercent; }
export function scoreValue(p: LiveCatalogProduct, sort: ResearchSort) { return sort.startsWith("AXIS:") ? axisValue(p, sort.slice(5)) : p.axisAssessment?.totalScore; }
export function sortValue(p: LiveCatalogProduct, sort: ResearchSort, price: (p: LiveCatalogProduct) => number | undefined) { return sort === "PRICE_ASC" || sort === "PRICE_DESC" ? price(p) : scoreValue(p, sort); }
export function sortResearchProducts(products: LiveCatalogProduct[], sort: ResearchSort, price: (p: LiveCatalogProduct) => number | undefined, cohort = products) {
  const order = new Map(cohort.map((p, i) => [p.candidateId, i]));
  return [...products].sort((a, b) => {
    const av = sortValue(a, sort, price), bv = sortValue(b, sort, price);
    if (av === undefined || bv === undefined) { if (av !== bv) return av === undefined ? 1 : -1; }
    else if (av !== bv) return sort === "PRICE_ASC" ? av - bv : bv - av;
    return (order.get(a.candidateId) ?? 0) - (order.get(b.candidateId) ?? 0) || a.candidateId.localeCompare(b.candidateId);
  });
}
export function comparativeStrength(product: LiveCatalogProduct, cohort: LiveCatalogProduct[], criteria?: ResearchCriteria | null) {
  const choices = (criteria?.axes ?? []).flatMap(axis => {
    const value = axisValue(product, axis.axisId); if (value === undefined) return [];
    const others = cohort.filter(p => p.candidateId !== product.candidateId).flatMap(p => { const v = axisValue(p, axis.axisId); return v === undefined ? [] : [v]; });
    if (!others.length) return [];
    const percentile = 100 * (others.filter(v => v < value).length + 0.5 * others.filter(v => v === value).length) / others.length;
    const rank = 1 + others.filter(v => v > value).length;
    const importance = product.axisAssessment?.criteria.axes.find(a => a.axisId === axis.axisId)?.importance ?? 0;
    return [{ axis, value, percentile, rank, count: others.length + 1, margin: value - others.reduce((a, b) => a + b, 0) / others.length, importance }];
  });
  return choices.sort((a, b) => b.percentile - a.percentile || b.margin - a.margin || b.importance - a.importance || a.axis.axisId.localeCompare(b.axis.axisId))[0];
}

/**
 * Vitlane Pick is the best score that fits the Target's budget (ADR-0086).
 * `withinBudget` answers true, false, or undefined when the price cannot be
 * compared with the budget. When nothing is known to fit, the best score leads,
 * exactly as before budgets existed. Every candidate tied with the Pick on
 * score and fit is a leader too.
 */
export function pickLeaders(cohort: LiveCatalogProduct[], withinBudget: (p: LiveCatalogProduct) => boolean | undefined): Set<string> {
  const scored = cohort.filter(p => p.axisAssessment !== undefined);
  const fitting = scored.filter(p => withinBudget(p) === true);
  const pool = fitting.length > 0 ? fitting : scored;
  const best = Math.max(...pool.map(p => p.axisAssessment!.totalScore));
  return new Set(pool.filter(p => p.axisAssessment!.totalScore === best).map(p => p.candidateId));
}
/** Vitlane Pick order: the leaders first, then every other candidate by score. */
export function sortByPick(products: LiveCatalogProduct[], cohort: LiveCatalogProduct[], withinBudget: (p: LiveCatalogProduct) => boolean | undefined): LiveCatalogProduct[] {
  const leaders = pickLeaders(cohort, withinBudget);
  const byScore = sortResearchProducts(products, "PICK", () => undefined, cohort);
  return [...byScore.filter(p => leaders.has(p.candidateId)), ...byScore.filter(p => !leaders.has(p.candidateId))];
}
