import { useCallback, useEffect, useRef, useState } from "react";
import {
  listCurations,
  type CurationListPage,
  type SidebarCuration,
} from "../infra/curationApi";

// A Curation this tab just created. The creation response already holds the
// row, so the sidebar shows it without asking the server (ADR-0079).
export const curationCreated = "vitlane:curation-created";

export function publishCurationCreated(curation: SidebarCuration): void {
  window.dispatchEvent(new CustomEvent<SidebarCuration>(curationCreated, { detail: curation }));
}

// A creation this tab could not confirm. The request failed without the server
// refusing it, so the Curation may have been saved while its response was lost;
// the sidebar checks for newer Curations instead of waiting for the next return.
export const curationCreationUnconfirmed = "vitlane:curation-creation-unconfirmed";

export function publishCurationCreationUnconfirmed(): void {
  window.dispatchEvent(new Event(curationCreationUnconfirmed));
}

// The sidebar renders on every authenticated route, so a response it cannot
// read counts as a failed read instead of breaking the whole shell.
async function readPage(query?: { before?: string; after?: string }): Promise<CurationListPage> {
  const page = await listCurations(query);
  if (!Array.isArray(page?.curations)) throw new Error("CURATION_LIST_UNREADABLE");
  return page;
}

export type SidebarCurationList = {
  curations: SidebarCuration[];
  nextCursor?: string;
  latestCursor?: string;
};

// The server orders every page newest first by creation time, and that order
// never changes, so merging only has to put pages in place and drop repeats.
export function firstSidebarPage(page: CurationListPage): SidebarCurationList {
  return { curations: page.curations, nextCursor: page.nextCursor, latestCursor: page.latestCursor };
}

export function withNewerCurations(list: SidebarCurationList, page: CurationListPage): SidebarCurationList {
  const latestCursor = page.latestCursor ?? list.latestCursor;
  // More new Curations than one page: what the list held is no longer adjacent
  // to them, so the list starts over from this page.
  if (page.nextCursor) return { curations: page.curations, nextCursor: page.nextCursor, latestCursor };
  if (page.curations.length === 0) return latestCursor === list.latestCursor ? list : { ...list, latestCursor };
  const fresh = new Set(page.curations.map(({ curationId }) => curationId));
  return {
    curations: [...page.curations, ...list.curations.filter(({ curationId }) => !fresh.has(curationId))],
    nextCursor: list.nextCursor,
    latestCursor,
  };
}

export function withOlderCurations(list: SidebarCurationList, page: CurationListPage): SidebarCurationList {
  const known = new Set(list.curations.map(({ curationId }) => curationId));
  return {
    curations: [...list.curations, ...page.curations.filter(({ curationId }) => !known.has(curationId))],
    nextCursor: page.nextCursor,
    latestCursor: list.latestCursor,
  };
}

export function withCreatedCuration(list: SidebarCurationList, curation: SidebarCuration): SidebarCurationList {
  return {
    ...list,
    curations: [curation, ...list.curations.filter(({ curationId }) => curationId !== curation.curationId)],
  };
}

export type SidebarCurations = {
  curations: SidebarCuration[];
  loading: boolean;
  failed: boolean;
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
};

// The sidebar reads its first page once, asks only for newer Curations when
// the tab becomes visible again or a creation ends unconfirmed, and inserts the
// ones this tab creates. It never polls and never re-reads on navigation, and a
// failed re-read keeps the list it already has.
export function useSidebarCurations(): SidebarCurations {
  const [list, setList] = useState<SidebarCurationList>({ curations: [] });
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const listRef = useRef(list);
  listRef.current = list;
  const loaded = useRef(false);
  const alive = useRef(true);
  const firstRead = useRef<Promise<void> | null>(null);
  const newerCheck = useRef<Promise<void> | null>(null);
  const extending = useRef(false);
  // Bumped whenever the list starts over, so an older page that was already on
  // its way cannot attach itself below rows it is no longer adjacent to.
  const generation = useRef(0);

  const apply = useCallback((next: (current: SidebarCurationList) => SidebarCurationList) => {
    setList((current) => {
      const updated = next(current);
      listRef.current = updated;
      return updated;
    });
  }, []);

  const readFirstPage = useCallback((): Promise<void> => {
    if (firstRead.current) return firstRead.current;
    const reading = readPage()
      .then((page) => {
        if (!alive.current) return;
        loaded.current = true;
        generation.current += 1;
        // A Curation this tab created while the read was on its way may be
        // missing from the page; keep it on top instead of dropping it.
        apply((current) => current.curations.reduceRight(
          (next, curation) => withCreatedCuration(next, curation),
          firstSidebarPage(page),
        ));
        setFailed(false);
      })
      .catch(() => {
        if (alive.current && !loaded.current) setFailed(true);
      })
      .finally(() => {
        firstRead.current = null;
        if (alive.current) setLoading(false);
      });
    firstRead.current = reading;
    return reading;
  }, [apply]);

  const checkForNewer = useCallback((): Promise<void> => {
    const after = listRef.current.latestCursor;
    // A tab whose first read failed treats coming back as the retry, and an
    // empty list has no newest row to ask after, so both read the first page.
    if (!loaded.current || !after) return readFirstPage();
    if (newerCheck.current) return newerCheck.current;
    const checking = readPage({ after })
      .then((page) => {
        if (!alive.current) return;
        if (page.nextCursor) generation.current += 1;
        apply((current) => withNewerCurations(current, page));
      })
      .catch(() => {
        // Keep the list the tab already shows; the next return tries again.
      })
      .finally(() => {
        newerCheck.current = null;
      });
    newerCheck.current = checking;
    return checking;
  }, [apply, readFirstPage]);

  useEffect(() => {
    alive.current = true;
    void readFirstPage();
    const visible = () => {
      if (document.visibilityState === "visible") void checkForNewer();
    };
    const created = (event: Event) => {
      const detail = (event as CustomEvent<SidebarCuration>).detail;
      if (detail?.curationId) apply((current) => withCreatedCuration(current, detail));
    };
    // A read already on its way may have left before the lost creation was
    // saved, so this waits for it and asks once more.
    const unconfirmed = () => {
      const pending = firstRead.current ?? newerCheck.current;
      void (pending ? pending.then(checkForNewer) : checkForNewer());
    };
    document.addEventListener("visibilitychange", visible);
    window.addEventListener("focus", visible);
    window.addEventListener(curationCreated, created);
    window.addEventListener(curationCreationUnconfirmed, unconfirmed);
    return () => {
      alive.current = false;
      document.removeEventListener("visibilitychange", visible);
      window.removeEventListener("focus", visible);
      window.removeEventListener(curationCreated, created);
      window.removeEventListener(curationCreationUnconfirmed, unconfirmed);
    };
  }, [apply, readFirstPage, checkForNewer]);

  const loadMore = useCallback(() => {
    const before = listRef.current.nextCursor;
    if (!before || extending.current) return;
    extending.current = true;
    const started = generation.current;
    setLoadingMore(true);
    void readPage({ before })
      .then((page) => {
        if (alive.current && started === generation.current) {
          apply((current) => withOlderCurations(current, page));
        }
      })
      .catch(() => {
        // The button stays, so the person can try again.
      })
      .finally(() => {
        extending.current = false;
        if (alive.current) setLoadingMore(false);
      });
  }, [apply]);

  return {
    curations: list.curations,
    loading,
    failed,
    hasMore: Boolean(list.nextCursor),
    loadingMore,
    loadMore,
  };
}
