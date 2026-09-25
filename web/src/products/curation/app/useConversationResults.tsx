import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";

/**
 * A response and the results it produced are drawn by different owners: the
 * transcript draws the response, and the research surface owns every candidate
 * state (pool, cart, options, reactions). This bridge lets the research surface
 * place each Target's result inside the response that holds it, and lets a
 * product name in a reply open that product, without moving any of that state.
 * Nothing opens inside the conversation: a Target's candidates live in its sheet.
 *
 * The Server saves no display facts of a Shopify product, so a reply's reference
 * to one arrives without a title. The research surface reads that product fresh
 * for its card anyway, and publishes the names it has so the reply can use them.
 */
type ConversationResults = {
  hosts: Readonly<Record<string, HTMLElement>>;
  registerHost: (threadId: string, node: HTMLElement | null) => void;
  openCandidate: (candidateId: string, targetId: string) => void;
  bindOpenCandidate: (open: (candidateId: string, targetId: string) => void) => () => void;
  /** Product names the research surface currently shows, by candidate. */
  titles: Readonly<Record<string, string>>;
  publishTitles: (titles: Readonly<Record<string, string>>) => void;
};

const ConversationResultsContext = createContext<ConversationResults | undefined>(undefined);
export const useConversationResults = () => useContext(ConversationResultsContext);

export function ConversationResultsProvider({ children }: { children: ReactNode }) {
  const [hosts, setHosts] = useState<Record<string, HTMLElement>>({});
  const opener = useRef<((candidateId: string, targetId: string) => void) | undefined>(undefined);
  const registerHost = useCallback((threadId: string, node: HTMLElement | null) => {
    setHosts(current => {
      if (node) return current[threadId] === node ? current : { ...current, [threadId]: node };
      if (!(threadId in current)) return current;
      const next = { ...current }; delete next[threadId]; return next;
    });
  }, []);
  const openCandidate = useCallback((candidateId: string, targetId: string) => opener.current?.(candidateId, targetId), []);
  const bindOpenCandidate = useCallback((open: (candidateId: string, targetId: string) => void) => {
    opener.current = open;
    return () => { if (opener.current === open) opener.current = undefined; };
  }, []);
  const [titles, setTitles] = useState<Readonly<Record<string, string>>>({});
  const publishTitles = useCallback((next: Readonly<Record<string, string>>) => {
    setTitles(current => {
      const keys = Object.keys(next);
      return keys.length === Object.keys(current).length && keys.every(key => current[key] === next[key]) ? current : next;
    });
  }, []);
  const value = useMemo(() => ({ hosts, registerHost, openCandidate, bindOpenCandidate, titles, publishTitles }), [hosts, registerHost, openCandidate, bindOpenCandidate, titles, publishTitles]);
  return <ConversationResultsContext.Provider value={value}>{children}</ConversationResultsContext.Provider>;
}
