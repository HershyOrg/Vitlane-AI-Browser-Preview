import { useCurationThreads } from "../app/useThreads";
import { BudgetProvider } from "../app/useBudget";
import { BudgetBar } from "./BudgetBar";
import { ResearchSettings } from "../research/iface/ResearchSettings";
import { ResearchCurrencyProvider } from "../research/app/useResearchCurrency";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { useLocale } from "../../../shared/i18n";
import type { CurationWorkspaceResponse } from "../domain/types";
import { executeAutoResearchAction } from "../infra/curationApi";
import { CurationComposer, type Focus } from "./CurationComposer";
import { CurationComposerDock } from "./CurationComposerDock";
import { CurationDismissibleNotice } from "./CurationDismissibleNotice";
import { CurationAgentWork } from "./CurationAgentWork";
import { CurationThreadProgress } from "./CurationThread";

export function PlanningComposer({
  response, working, onBusyChange, onAddTargets, onWorkChanged,
}: {
  response: CurationWorkspaceResponse;
  working: boolean;
  onBusyChange: (busy: boolean) => void;
  onAddTargets: (instruction: string) => Promise<void>;
  onWorkChanged?: () => void | Promise<void>;
}) {
  const { l } = useLocale();
  const threads = useCurationThreads();
  const [focus, setFocus] = useState<Focus>({ kind: "AUTO" });
  const [instruction, setInstruction] = useState("");
  const [modeSelectorOpen, setModeSelectorOpen] = useState(false);
  const [error, setError] = useState<string>();
  const [budgetNotice, setBudgetNotice] = useState<string>();
  const [selectionMessage, setSelectionMessage] = useState<string>();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const pending = useRef(false);

  useEffect(() => { if (threads?.ready) setFocus(current => threads.mode.mode === "AUTO" ? { kind: "AUTO" } : current.kind === "AUTO" ? { kind: "ADD_TARGET" } : current); }, [threads?.ready, threads?.mode.mode]);
  // Work that a request Thread owns is reported by the Thread bar above the
  // composer. Only pre-Thread (legacy) Jobs still use the step panel.
  const coveredActions = new Set(threads?.threads.flatMap(t => t.actions.flatMap(a => [a.id, ...a.jobs.map(j => j.actionId)])) ?? []);
  const legacyJobs = (response.intelligence ?? []).filter(j => !coveredActions.has(j.actionId));

  function selectFocus(value: Focus) {
    setFocus(value);
    setModeSelectorOpen(false);
    setSelectionMessage(undefined);
    window.requestAnimationFrame(() => inputRef.current?.focus());
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = instruction.trim();
    if (working || threads?.busy || pending.current || !value) return;
    pending.current = true;
    onBusyChange(true);
    setError(undefined);
    setBudgetNotice(undefined);
    try {
      if (threads) {
        await threads.submit(value, response.curation.version, threads.mode.mode === "AUTO" ? undefined : "ADD_TARGET");
        setInstruction(""); await onWorkChanged?.(); return;
      }
      if (focus.kind === "AUTO") {
        const result = await executeAutoResearchAction({
          curationId: response.curation.id,
          request: value,
          expectedCurationVersion: response.curation.version,
        });
        if (result.reasonCode === "BUDGET_SETTINGS_ONLY") {
          setBudgetNotice(l("Edit budgets using the Budget button above the input.", "예산은 입력창 위 예산 버튼에서 변경해 주세요."));
          setInstruction("");
          return;
        }
        if (result.status === "NEEDS_SELECTION") {
          setSelectionMessage(l(
            "Choose Add product to continue planning.",
            "상품 추가를 선택해 조사 항목을 구성해 주세요.",
          ));
          setModeSelectorOpen(true);
          return;
        }
        await onWorkChanged?.();
      } else {
        await onAddTargets(value);
      }
      setInstruction("");
      setFocus({ kind: "AUTO" });
    } catch {
      setError(l(
        "We couldn't send the request. Your text is preserved; try again.",
        "요청을 보내지 못했습니다. 입력 내용은 유지됩니다. 다시 시도해 주세요.",
      ));
    } finally {
      pending.current = false;
      onBusyChange(false);
    }
  }

  return (
    <ResearchCurrencyProvider curationId={response.curation.id}><BudgetProvider key={response.curation.id} curationId={response.curation.id} targets={response.targets}><CurationComposerDock progress={
      threads?.active || legacyJobs.length === 0 ? undefined : <CurationAgentWork jobs={legacyJobs} activeWork={response.activeWork} onWorkChanged={onWorkChanged} />
    }>
      <CurationThreadProgress />
      {budgetNotice && <p role="status" className="budget-settings-hint">{budgetNotice}</p>}
      {error ? (
        <CurationDismissibleNotice tone="danger" noticeId={error}>
          {error}
        </CurationDismissibleNotice>
      ) : null}
      <CurationComposer
        country={response.plan.locationContext.country}
        contextSettings={<BudgetBar settings={<ResearchSettings key={response.curation.id} curationId={response.curation.id} compact />} />}
        focus={focus}
        selectFocus={selectFocus}
        busy={working || Boolean(threads?.busy)}
        modeSelectorOpen={modeSelectorOpen}
        setModeSelectorOpen={setModeSelectorOpen}
        autoResolutionMessage={selectionMessage}
        composerRef={inputRef}
        instruction={instruction}
        setInstruction={setInstruction}
        submitComposer={submit}
      />
    </CurationComposerDock></BudgetProvider></ResearchCurrencyProvider>
  );
}
