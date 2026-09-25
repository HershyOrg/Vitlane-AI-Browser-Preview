import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { request } from "../../../shared/api/client";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { curationThreadsChangedEvent, type CurationThreadsChangedDetail } from "../infra/curationApi";
import { type ControlMode, type CurationThread, threadActive, currentAction } from "../domain/thread";

const normalizeThread = (t: CurationThread): CurationThread => ({...t, targetLabels: t.targetLabels ?? {}, actions: (t.actions ?? []).map(a=>({...a,jobs:(a.jobs ?? []).map(j=>({...j,effects:j.effects ?? []})),effects:a.effects ?? [],decisions:a.decisions ?? [],answers:a.answers ?? [],decisionIds:a.decisionIds ?? []}))});

// ADR-0081: the Server changes a Thread on its own only while it interprets or
// runs, so only then, and only while the tab is seen, is the list polled. Every
// other change is read when it happens: tab return, focus, a Thread-touching call.
export const THREAD_POLL_INTERVAL_MS = 2_000;
export const THREAD_READ_RETRY_DELAYS_MS = [2_000, 5_000, 10_000] as const;
export const threadPolled = (t: CurationThread) => t.status === "INTERPRETING" || t.status === "RUNNING";
const pageVisible = () => typeof document === "undefined" || document.visibilityState !== "hidden";
type ThreadList = { controlMode?: ControlMode; threads: CurationThread[] };
// "share" joins a read already on its way. "after" follows a change that may
// have landed after that read left, so it reads once more when that read returns.
type ReadKind = "share" | "after";

type ThreadController = {
 ready: boolean; error: boolean; busy: boolean; sending: boolean; mode: ControlMode;
 threads: CurationThread[]; active?: CurationThread; revision: string;
 reload: () => Promise<void>;
 changeMode: (mode: ControlMode["mode"]) => Promise<void>;
 submit: (text: string, version: number, kind?: "ADD_TARGET" | "RESEARCH_AGAIN" | "COMBINATION", targetId?: string) => Promise<void>;
 cancel: (thread: CurationThread) => Promise<void>;
 cancelAction: (thread: CurationThread, actionId:string) => Promise<void>;
 answer: (thread: CurationThread, optionId?: string, text?: string) => Promise<void>;
 sheetOpen: (threadId: string) => boolean | undefined;
 rememberSheet: (threadId: string, open: boolean) => void;
};
const Context = createContext<ThreadController | null>(null);
export const useCurationThreads = () => useContext(Context);
export function CurationThreadProvider({ curationId, children }: { curationId: string; children: ReactNode }) {
 const [mode, setMode] = useState<ControlMode>({ mode: "AUTO", version: 1 });
 const [threads, setThreads] = useState<CurationThread[]>([]);
 const [ready, setReady] = useState(false); const [error, setError] = useState(false); const [sending, setSending] = useState(false);
 const [visible, setVisible] = useState(pageVisible);
 const alive = useRef(true); const generation = useRef(0); const mutating = useRef(false); const pending = useRef<{ hash: string; id: string; version: number } | undefined>(undefined);
 const reading = useRef<Promise<void> | null>(null); const readAgain = useRef(false); const polling = useRef(false); const loaded = useRef(false);
 const failures = useRef(0); const retryTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
 const base = `/api/v1/curations/${encodeURIComponent(curationId)}`;
 // The Planning and the Curating screens each mount the Thread bar; an open sheet stays open when research starts.
 const sheets = useRef(new Map<string, boolean>());
 const reload = useCallback((kind: ReadKind = "after"): Promise<void> => {
  if (reading.current) { if (kind === "after") readAgain.current = true; return reading.current; }
  const read = async () => {
   do {
    readAgain.current = false; const epoch = generation.current;
    try {
     const result = await request<ThreadList>(`${base}/threads`);
     // A read that left before a mutation settled is stale; the mutation reads again.
     if (!alive.current || epoch !== generation.current) continue;
     const next = result.threads.map(normalizeThread);
     const nextMode = result.controlMode;
     if (nextMode) setMode(current => current.mode === nextMode.mode && current.version === nextMode.version ? current : nextMode);
     setThreads(current => JSON.stringify(current) === JSON.stringify(next) ? current : next);
     setReady(true); setError(false); loaded.current = true; failures.current = 0; clearTimeout(retryTimer.current);
    } catch {
     if (!alive.current) return;
     setError(true);
     // Without a poll nothing else tries again. The composer stays locked until
     // the first list lands, so that read keeps retrying; later failures try a
     // few times and then wait for tab return, the next change or 다시 확인.
     const attempt = failures.current++;
     const delays = THREAD_READ_RETRY_DELAYS_MS;
     if (!polling.current && (!loaded.current || attempt < delays.length)) { clearTimeout(retryTimer.current); retryTimer.current = setTimeout(() => void reload("share"), delays[Math.min(attempt, delays.length - 1)]); }
    }
   } while (readAgain.current && alive.current);
  };
  const started = read().finally(() => { reading.current = null; });
  reading.current = started;
  return started;
 }, [base]);
 useEffect(() => {
  alive.current = true; void reload("share");
  const seen = () => { const now = pageVisible(); setVisible(now); if (now) void reload("share"); };
  const focused = () => void reload("share");
  const changed = (event: Event) => { const id = (event as CustomEvent<CurationThreadsChangedDetail>).detail?.curationId; if (!id || id === curationId) void reload("after"); };
  document.addEventListener("visibilitychange", seen); window.addEventListener("focus", focused); window.addEventListener(curationThreadsChangedEvent, changed);
  return () => { alive.current = false; clearTimeout(retryTimer.current); document.removeEventListener("visibilitychange", seen); window.removeEventListener("focus", focused); window.removeEventListener(curationThreadsChangedEvent, changed); };
 }, [curationId, reload]);
 const watching = visible && threads.some(threadPolled);
 useEffect(() => {
  polling.current = watching;
  if (!watching) return;
  const timer = setInterval(() => void reload("share"), THREAD_POLL_INTERVAL_MS);
  return () => { polling.current = false; clearInterval(timer); };
 }, [watching, reload]);
 const active = threads.find(threadActive);
 // Read after failures too: a lost response may have been stored, and a
 // rejection may be another window's active Thread that this tab has not seen.
 const mutate = async (run: () => Promise<unknown>) => { if (mutating.current) throw new Error("Request in progress"); mutating.current = true; generation.current++; setSending(true); try { await run(); } finally { generation.current++; await reload("after"); mutating.current = false; if (alive.current) setSending(false); } };
 const value: ThreadController = { ready, error, sending, mode, threads, active, busy: !ready || sending || Boolean(active), revision: threads.map(t => `${t.id}:${t.updatedAt}`).join("|"), reload: () => reload("after"),
  changeMode: next => mutate(async () => { const result = await request<ControlMode>(`${base}/control-mode`, { method: "PUT", body: JSON.stringify({ mode: next, version: mode.version }) }); if (alive.current) setMode(result); }),
  submit: (text, version, kind, targetId) => mutate(async () => {
   const hash = JSON.stringify({ text, kind, targetId });
   const storageKey = `vitlane.thread.pending:${curationId}`;
   try { if (!pending.current) pending.current = JSON.parse(sessionStorage.getItem(storageKey) ?? "null") ?? undefined; } catch { /* storage is optional */ }
   const id = pending.current?.hash === hash ? pending.current.id : randomUUID(); const requestVersion = pending.current?.hash === hash ? pending.current.version : version; pending.current = { hash, id, version: requestVersion };
   try { sessionStorage.setItem(storageKey, JSON.stringify(pending.current)); } catch { /* storage is optional */ }
   const t = await request<CurationThread>(`${base}/threads`, { method: "POST", headers: { "Idempotency-Key": id }, body: JSON.stringify({ request: text, expectedCurationVersion: requestVersion, clientRequestId: id, kind, targetId }) });
   pending.current = undefined; try { sessionStorage.removeItem(storageKey); } catch { /* storage is optional */ } if (alive.current) setThreads(current => [normalizeThread(t), ...current.filter(v => v.id !== t.id)]);
  }),
  cancel: t => mutate(() => request(`${base}/threads/${t.id}/cancel`, { method: "POST", body: "{}" })),
  cancelAction: (t,actionId)=>mutate(()=>request(`${base}/threads/${t.id}/actions/${actionId}/cancel`,{method:"POST",body:"{}"})),
  answer: (t, optionId, text) => mutate(() => request(`${base}/threads/${t.id}/answer`, { method: "POST", body: JSON.stringify({ revision: t.revision, questionId: currentAction(t)?.question?.id, optionId, text }) })),
  sheetOpen: id => sheets.current.get(id),
  rememberSheet: (id, open) => { sheets.current.set(id, open); },
 };
 return <Context.Provider value={value}>{children}</Context.Provider>;
}
