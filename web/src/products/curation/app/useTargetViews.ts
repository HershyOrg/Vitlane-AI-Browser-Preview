import { useCallback, useEffect, useState } from "react";
import type { RepresentativeSignal } from "../domain/conversationResults";
import type { ResearchSort } from "../domain/researchCriteria";

/**
 * How the reader is looking at each Target: the sort they chose and what they last opened. It decides
 * which product a response shows for the Target (ADR-0086), and it is a convenience of this device —
 * nothing the Server or another device depends on — so it lives in localStorage and the screen works
 * the same without it (private windows, blocked site data).
 */
export type TargetView = { sort?: ResearchSort; signal?: RepresentativeSignal };
export type TargetViews = Record<string, TargetView>;

const storageKey = (curationId: string) => `vitlane.curation.target-views.v1:${curationId}`;

function read(curationId: string): TargetViews {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(storageKey(curationId)) ?? "{}");
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const views: TargetViews = {};
    for (const [targetId, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (!value || typeof value !== "object") continue;
      const { sort, signal } = value as { sort?: unknown; signal?: { kind?: unknown; candidateId?: unknown; at?: unknown } };
      const view: TargetView = {};
      if (typeof sort === "string") view.sort = sort as ResearchSort;
      if (signal && typeof signal.at === "string") {
        if (signal.kind === "SORT") view.signal = { kind: "SORT", at: signal.at };
        if (signal.kind === "VIEWED" && typeof signal.candidateId === "string") view.signal = { kind: "VIEWED", candidateId: signal.candidateId, at: signal.at };
      }
      views[targetId] = view;
    }
    return views;
  } catch { return {}; }
}

export function useTargetViews(curationId: string, now: () => Date = () => new Date()) {
  const [views, setViews] = useState<TargetViews>(() => read(curationId));
  useEffect(() => { setViews(read(curationId)); }, [curationId]);
  const update = useCallback((targetId: string, change: (view: TargetView) => TargetView) => {
    setViews(current => {
      const next = { ...current, [targetId]: change(current[targetId] ?? {}) };
      try { window.localStorage.setItem(storageKey(curationId), JSON.stringify(next)); } catch { /* the view still applies to this visit */ }
      return next;
    });
  }, [curationId]);
  // Choosing a sort — even the one already shown — says "show me its leader", so it replaces an earlier view.
  const chooseSort = useCallback((targetId: string, sort: ResearchSort) => update(targetId, () => ({ sort, signal: { kind: "SORT", at: now().toISOString() } })), [update, now]);
  const recordViewed = useCallback((targetId: string, candidateId: string) => update(targetId, view => ({ ...view, signal: { kind: "VIEWED", candidateId, at: now().toISOString() } })), [update, now]);
  return { views, chooseSort, recordViewed };
}
