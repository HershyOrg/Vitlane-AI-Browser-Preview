import { useCurationThreads } from "../app/useThreads";
import { ArrowUpDown } from "lucide-react";
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { request, APIError } from "../../../shared/api/client";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { Button, Disclosure, Input, Popover, PopoverTrigger, PopoverContent, DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
import { useResearchCurrency } from "../research/app/useResearchCurrency";
import { convertResearchMinor } from "../research/domain/exchangeRate";
import { comparativeStrength, pickLeaders, sortByPick, sortResearchProducts, sortValue, type AxisAssessment, type ResearchAxis, type ResearchCriteria, type ResearchSort } from "../domain/researchCriteria";
import { productPrice, type CandidatePrice } from "../domain/candidatePresentation";
import { useBudgetFit } from "./BudgetViews";
import "./research-comparison.css";
export const criteriaPath = (curation: string, target: string) => `/api/v1/curations/${encodeURIComponent(curation)}/targets/${encodeURIComponent(target)}/criteria`;
export function useTargetCriteria(curation: string, targets: string[], revision: string) {
  const [criteria, setCriteria] = useState<Record<string, ResearchCriteria | null>>({});
  const identity = targets.join(",");
  useEffect(() => { let active = true; for (const id of identity.split(",").filter(Boolean)) void request<ResearchCriteria | null>(criteriaPath(curation, id)).then(value => { if (active) setCriteria(previous => ({ ...previous, [id]: value })); }).catch(() => {}); return () => { active = false; }; }, [curation, identity, revision]);
  return { criteria, set: (id: string, value: ResearchCriteria) => setCriteria(previous => ({ ...previous, [id]: value })) };
}
type CriteriaEditorProps = { criteria: ResearchCriteria | null | undefined; curationId: string; targetId: string; version: number; onSave: (value: ResearchCriteria) => void };
export function CriteriaEditor(props: CriteriaEditorProps) {
  return <div className="research-criteria"><div className="research-criteria__chips">
    {props.criteria?.axes.map(axis => <AxisEditor key={axis.axisId} {...props} axis={axis} />)}
    <AxisEditor {...props} />
  </div></div>;
}
function AxisEditor({ criteria, curationId, targetId, version, onSave, axis }: CriteriaEditorProps & { axis?: ResearchAxis }) {
  const { l } = useLocale();
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState("");
  const [importance, setImportance] = useState(3);
  const [usesPrice, setUsesPrice] = useState(false);
  const [usesVisualEvidence, setUsesVisualEvidence] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef<{ signature: string; body: string } | undefined>(undefined);
  const thread=useCurationThreads();
  const unavailable = Boolean(thread?.busy) || !criteria || (!axis && criteria.axes.length >= 8);
  useEffect(() => {
    if (!open) return;
    setLabel(axis?.label ?? ""); setImportance(axis?.importance ?? 3);
    setUsesPrice(axis?.usesPrice ?? false); setUsesVisualEvidence(axis?.usesVisualEvidence ?? false);
    setError(""); pending.current = undefined;
  }, [open, axis]);
  async function save(remove = false) {
    if (!criteria || busy || thread?.busy || (!remove && !label.trim())) return;
    const edited: ResearchAxis = axis ? { ...axis, importance } : { axisId: "", label: label.trim(), definition: label.trim(), importance, usesPrice, usesVisualEvidence, origin: "USER_EDIT" };
    const signature = JSON.stringify({ criteria, edited, remove, version });
    if (pending.current?.signature !== signature) {
      if (!axis) edited.axisId = randomUUID();
      const axes = axis ? criteria.axes.flatMap(a => a.axisId === axis.axisId ? remove ? [] : [edited] : [a]) : [...criteria.axes, edited];
      pending.current = { signature, body: JSON.stringify({ schemaVersion: "vitlane.criteria-command.v1", expectedCriteriaVersion: criteria.version, expectedCurationVersion: version, idempotencyKey: randomUUID(), criteria: { ...criteria, axes } }) };
    }
    setBusy(true); setError("");
    try {
      const value = await request<ResearchCriteria>(criteriaPath(curationId, targetId), { method: "PUT", body: pending.current.body });
      pending.current = undefined; onSave(value); setOpen(false);
    } catch (e) {
      if (e instanceof APIError && e.status === 409) { pending.current = undefined; setError(l("Criteria changed. Reload before editing again.", "기준이 변경되었습니다. 새로고침한 뒤 다시 수정해 주세요.")); }
      else setError(l("Couldn't save. Try again.", "저장하지 못했습니다. 다시 시도해 주세요."));
    } finally { setBusy(false); }
  }
  return <Popover open={open} onOpenChange={value => { if (!busy) setOpen(value); }}>
    <PopoverTrigger asChild><Button className="research-axis-trigger" data-importance={axis?.importance} type="button" emphasis="quiet" disabled={unavailable || busy}
      aria-label={axis ? l("Edit {axis}, importance {value}", "{axis} 편집, 중요도 {value}", { axis: axis.label, value: axis.importance }) : l("Add criterion", "비교 기준 추가")}
      title={unavailable ? l("Up to eight criteria; available after the first research.", "첫 조사 후 최대 8개까지 설정할 수 있습니다.") : undefined}>
      {axis ? <><span className="research-axis-trigger__label">{l("#{axis}", "#{axis}", { axis: axis.label })}</span><span className="research-axis-popover__weight">{axis.importance}</span></> : <span className="research-axis-popover__plus" aria-hidden="true">+</span>}
    </Button></PopoverTrigger>
    <PopoverContent className="research-criteria__popover" align="start" collisionPadding={12}>
    <form className="research-criteria__editor" aria-label={axis ? l("Edit criterion", "비교 기준 수정") : l("Add criterion", "비교 기준 추가")} onSubmit={e => { e.preventDefault(); void save(); }}>
      {axis ? <h3>{axis.label}</h3> : <label>{l("Criterion", "비교 기준")}<Input autoFocus required maxLength={40} value={label} disabled={busy || Boolean(thread?.busy)} placeholder={l("e.g. Writing comfort", "예: 필기감")} onChange={e => setLabel(e.target.value)} /></label>}
      <fieldset disabled={busy || Boolean(thread?.busy)}><legend>{l("Importance", "중요도")}</legend><div className="research-criteria__importance" role="group" aria-label={l("Importance", "중요도")}>
        {[1, 2, 3, 4, 5].map(value => <Button key={value} type="button" emphasis="quiet" aria-pressed={importance === value} onClick={() => setImportance(value)}>{value}</Button>)}
      </div></fieldset>
      {!axis && <div className="research-criteria__options"><label><Input type="checkbox" disabled={busy || Boolean(thread?.busy)} checked={usesPrice} onChange={e => setUsesPrice(e.target.checked)} />{l("Compare price or value", "가격·가성비 비교")}</label>
        <label><Input type="checkbox" disabled={busy || Boolean(thread?.busy)} checked={usesVisualEvidence} onChange={e => setUsesVisualEvidence(e.target.checked)} />{l("Compare appearance", "외관 비교")}</label></div>}
      {error && <p role="alert">{error}</p>}
      <div className="research-criteria__actions">
        {axis && <Button type="button" emphasis="quiet" disabled={busy || criteria!.axes.length <= 1} onClick={() => void save(true)}>{l("Remove", "삭제")}</Button>}
        <Button type="button" emphasis="quiet" disabled={busy || Boolean(thread?.busy)} onClick={() => setOpen(false)}>{l("Cancel", "취소")}</Button>
        <Button type="submit" emphasis="secondary" busy={busy} disabled={!label.trim() || Boolean(thread?.busy)}>{l("Save", "저장")}</Button>
      </div>
    </form>
    </PopoverContent>
  </Popover>;
}
const EvaluationContext = createContext<{ cohort: LiveCatalogProduct[]; criteria?: ResearchCriteria | null; sort: ResearchSort; leaders: Set<string> }>({ cohort: [], sort: "PICK", leaders: new Set() });
/**
 * One Target's candidates in the sort the reader chose. The Target sheet and the representative a
 * response shows both read this, so "the leader of this sort" means the same product in both places.
 * Vitlane Pick is the best score that fits the Target's budget (ADR-0086); every other sort keeps its plain leader.
 */
export function useTargetOrdering(cohort: LiveCatalogProduct[], criteria: ResearchCriteria | null | undefined, sort: ResearchSort, targetId?: string, priceOf: (p: LiveCatalogProduct) => CandidatePrice = productPrice, combinationCandidateId?: string) {
  const { currency, exchange } = useResearchCurrency();
  const fit = useBudgetFit(targetId);
  const price = (p: LiveCatalogProduct) => { const value = priceOf(p); if (value.kind === "UNKNOWN") return undefined; const n = value.kind === "OBSERVED" ? value.amountMinor : value.minimumMinor; return value.currency === currency ? n : exchange?.rate ? convertResearchMinor(n, value.currency, currency, exchange.rate) : undefined; };
  const effectiveSort: ResearchSort = sort === "COMBINATION" && !combinationCandidateId ? "PICK" : sort.startsWith("AXIS:") && !criteria?.axes.some(a => `AXIS:${a.axisId}` === sort) ? "PICK" : sort;
  const withinBudget = (p: LiveCatalogProduct) => fit(priceOf(p));
  const scored = cohort.some(p => p.axisAssessment !== undefined);
  const first = sortResearchProducts(cohort, effectiveSort, price)[0]; const best = first ? sortValue(first, effectiveSort, price) : undefined;
  const leaders = effectiveSort === "COMBINATION" ? new Set(cohort.filter(p=>p.candidateId===combinationCandidateId).map(p=>p.candidateId)) : effectiveSort === "PICK" && scored ? pickLeaders(cohort, withinBudget) : new Set(best === undefined ? [] : cohort.filter(p => sortValue(p, effectiveSort, price) === best).map(p => p.candidateId));
  const order = (products: LiveCatalogProduct[]) => effectiveSort === "COMBINATION" ? sortByPick(products,cohort,withinBudget).sort((a,b)=>Number(b.candidateId===combinationCandidateId)-Number(a.candidateId===combinationCandidateId)) : effectiveSort === "PICK" && scored ? sortByPick(products, cohort, withinBudget) : sortResearchProducts(products, effectiveSort, price, cohort);
  return { effectiveSort, leaders, order };
}
export function researchSortOptions(criteria: ResearchCriteria | null | undefined, l: ReturnType<typeof useLocale>["l"], combination = false): Array<{ value: ResearchSort; label: string }> {
  return [...(combination ? [{value:"COMBINATION" as const,label:l("Combination fit","조합 어울림")}] : []), { value: "PICK", label: l("Vitlane Pick", "Vitlane Pick") }, { value: "PRICE_ASC", label: l("Lowest price", "낮은 가격순") }, { value: "PRICE_DESC", label: l("Highest price", "높은 가격순") }, ...(criteria?.axes ?? []).map(a => ({ value: `AXIS:${a.axisId}` as ResearchSort, label: a.label }))];
}
/**
 * `sort` and `onSortChange` let the owner keep the choice (the Target sheet remembers it per Target); without them the sort lives here.
 * `bare` orders and badges the candidates by that choice without drawing the sort control: the sheet's "All" tab lists every
 * product group in the order its own tab chose, and changing a sort belongs to that tab.
 */
export function TargetComparison({ products, cohort, criteria, sort: chosenSort, onSortChange, controls, bare, priceOf, combinationCandidateId, children }: { products: LiveCatalogProduct[]; cohort: LiveCatalogProduct[]; criteria?: ResearchCriteria | null; sort?: ResearchSort; onSortChange?: (sort: ResearchSort) => void; controls?: ReactNode; bare?: boolean; combinationCandidateId?: string; priceOf?: (p: LiveCatalogProduct) => CandidatePrice; children: (sorted: LiveCatalogProduct[]) => ReactNode }) {
  const { l } = useLocale(); const [ownSort, setOwnSort] = useState<ResearchSort>("PICK"); const [open, setOpen] = useState(false);
  const { effectiveSort, leaders, order } = useTargetOrdering(cohort, criteria, chosenSort ?? ownSort, undefined, priceOf, combinationCandidateId);
  const options = researchSortOptions(criteria, l, Boolean(combinationCandidateId));
  if (bare) return <EvaluationContext.Provider value={{ cohort, criteria, sort: effectiveSort, leaders }}>{children(order(products))}</EvaluationContext.Provider>;
  return <EvaluationContext.Provider value={{ cohort, criteria, sort: effectiveSort, leaders }}><div className="research-comparison-controls">
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild><Button className="research-sort__trigger" type="button" emphasis="quiet" aria-label={l("Sort", "정렬")}><ArrowUpDown size={16} aria-hidden="true" /><span>{l("Sort · {value}", "정렬 · {value}", { value: options.find(o => o.value === effectiveSort)!.label })}</span></Button></DropdownMenuTrigger>
      <DropdownMenuContent className="research-sort__menu" align="start" collisionPadding={12} aria-label={l("Sort", "정렬")}>
        <DropdownMenuRadioGroup value={effectiveSort} onValueChange={value => { setOwnSort(value as ResearchSort); onSortChange?.(value as ResearchSort); }}>
          {options.map(option => <DropdownMenuRadioItem key={option.value} value={option.value}>{option.label}</DropdownMenuRadioItem>)}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
    {controls}
  </div>{children(order(products))}</EvaluationContext.Provider>;

}
export function useCandidateEvaluation(id: string) {
  const context = useContext(EvaluationContext); const product = context.cohort.find(p => p.candidateId === id); const strength = product ? comparativeStrength(product, context.cohort, context.criteria) : undefined;
  return { ...context, assessment: product?.axisAssessment, strength, leader: context.leaders.has(id), selectedAxis: context.criteria?.axes.find(a => `AXIS:${a.axisId}` === context.sort) };
}
export function CandidateEvaluationDetails({ assessment }: { assessment?: AxisAssessment }) {
  const { l } = useLocale();
  return <Disclosure className="candidate-detail__section candidate-axis-details" defaultOpen summary={l("Assessment at discovery", "조사 당시 평가")}>
    {!assessment ? <p>{l("No axis assessment was saved for this candidate.", "이 후보에는 저장된 축별 평가가 없습니다.")}</p> :
      <div className="candidate-axis-details__axes">{assessment.criteria.axes.map((axis) => {
        const score = assessment.scores.find(s => s.axisId === axis.axisId);
        if (!score) return null;
        return <div className="candidate-axis-details__axis" key={axis.axisId}>
          <span className="candidate-axis-details__label">{axis.label}</span>
          <span className="candidate-axis-details__score">{score.scorePercent}<small>/100</small></span>
          <p>{score.explanation}</p>
        </div>;
      })}</div>}
  </Disclosure>;
}
