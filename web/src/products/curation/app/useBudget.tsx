import { useCurationThreads } from "./useThreads";
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { APIError, request } from "../../../shared/api/client";
import type { PlanTarget } from "../../../shared/api/types";
import { budgetSchema, type BudgetCommand, type BudgetLedger } from "../domain/budget";

type BudgetContextValue = { ledger?: BudgetLedger; targets: Pick<PlanTarget, "id" | "title">[]; error: boolean; curationId: string; reload: () => Promise<void>; save: (command: BudgetCommand) => Promise<BudgetLedger> };
const BudgetContext = createContext<BudgetContextValue>({ targets: [], error: false, curationId: "", reload: async () => {}, save: async () => { throw new Error("Budget context unavailable"); } });
export const BudgetTargetContext = createContext<string | undefined>(undefined);
export const useBudget = () => useContext(BudgetContext);
export const useBudgetTarget = () => useContext(BudgetTargetContext);

export function BudgetProvider({ curationId, targets, children }: { curationId: string; targets: Pick<PlanTarget, "id" | "title">[]; children: ReactNode }) {
  const threads = useCurationThreads();
  const [ledger, setLedger] = useState<BudgetLedger>();
  const [error, setError] = useState(false);
  const generation = useRef(0);
  const path = `/api/v1/curations/${encodeURIComponent(curationId)}/budget`;
  const reload = useCallback(async () => {
    const current = ++generation.current;
    try { const next = await request<BudgetLedger>(path); if (next.schemaVersion !== budgetSchema || !Array.isArray(next.allocations)) throw new Error("Invalid budget response"); if (current === generation.current) { setLedger(previous => previous && previous.version > next.version ? previous : next); setError(false); } }
    catch { if (current === generation.current) setError(true); }
  }, [path]);
  const membership = targets.map(t => t.id).join(",");
  useEffect(() => { void reload(); return () => { generation.current++; }; }, [reload, membership, threads?.revision]);
  const save = async (command: BudgetCommand) => {
    try { const next = await request<BudgetLedger>(path, { method: "PATCH", body: JSON.stringify(command) }); generation.current++; setLedger(next); setError(false); return next; }
    catch (error) { if (error instanceof APIError && error.status === 409) await reload(); throw error; }
  };
  return <BudgetContext.Provider value={{ ledger, targets, error, curationId, reload, save }}>{children}</BudgetContext.Provider>;
}
