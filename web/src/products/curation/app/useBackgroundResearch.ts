import { useCallback, useEffect, useRef, useState } from "react";
import { backgroundChanged, readBackground, type BackgroundView, type ResearchFinding } from "../infra/backgroundApi";

export type DiscoveryResponse = { id: string; createdAt: string; subject: string; findings: ResearchFinding[] };

/** Saved discovery times, never the poll time: adding a candidate does not move its reply. */
export function discoveryResponses(view: BackgroundView | null): DiscoveryResponse[] {
  const groups = new Map<string, DiscoveryResponse>();
  for (const finding of [...(view?.findings ?? [])].sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt) || a.id.localeCompare(b.id))) {
    if (finding.status === "HIDDEN") continue;
    const id = `${finding.subscriptionId}:${Math.floor(Date.parse(finding.createdAt) / 60000)}`;
    const group = groups.get(id);
    if (group) group.findings.push(finding);
    else groups.set(id, { id, createdAt: finding.createdAt, subject: view?.subscriptions.find(s => s.id === finding.subscriptionId)?.terms.criteria.subject.label ?? "", findings: [finding] });
  }
  return [...groups.values()];
}

export function useBackgroundResearch(curationId: string, revision?: string) {
  const [view, setView] = useState<BackgroundView | null>(null);
  const requestVersion = useRef(0);
  const current = useRef(curationId); current.current = curationId;
  const refresh = useCallback(async () => {
    const version = ++requestVersion.current;
    const next = await readBackground(curationId);
    if (current.current === curationId && version === requestVersion.current && next?.schemaVersion === "vitlane.background-research.v1") setView(next);
  }, [curationId]);
  useEffect(() => { setView(null); return () => { ++requestVersion.current; }; }, [curationId]);
  useEffect(() => {
    const update = () => { void refresh().catch(() => {}); };
    const changed = (event: Event) => { const id = (event as CustomEvent<string>).detail; if (!id || id === curationId) update(); };
    update(); window.addEventListener(backgroundChanged, changed);
    return () => { window.removeEventListener(backgroundChanged, changed); };
  }, [curationId, revision, refresh]);
  return { view, refresh };
}
