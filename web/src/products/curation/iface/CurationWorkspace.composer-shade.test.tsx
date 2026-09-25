// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { CurationWorkspaceModel } from "../domain/types";

vi.mock("../app/useThreads", () => ({
  useCurationThreads: () => ({ threads: [], active: undefined, busy: false, reload: async () => undefined }),
}));
vi.mock("../app/useBackgroundResearch", () => ({
  useBackgroundResearch: () => ({ view: undefined, refresh: () => undefined }),
  discoveryResponses: () => [],
}));

import { CurationWorkspace } from "./CurationWorkspace";

const workspace = {
  curation: { id: "curation-1", title: "필기구", phase: "CURATING", version: 4, coverage: "PARTIAL", updatedAt: "2026-09-23T03:00:00Z" },
  curations: [],
  availableActions: [],
  timeline: [],
  cart: { curationId: "curation-1", selections: [], total: { amount: "0", currency: "KRW" }, warnings: [] },
} as unknown as CurationWorkspaceModel;

let root: Root | undefined;
let scroller: HTMLDivElement | undefined;
afterEach(async () => {
  if (root) await act(async () => root!.unmount());
  scroller?.remove(); root = undefined; scroller = undefined;
});

// The composer's shade lifts it off the conversation that scrolls under it (ADR-0089). At the end of the
// conversation — or when there is nothing to scroll — nothing is under it, and the shade goes (owner 2026-09-23).
it("marks the composer host at the end of the conversation, and only there", async () => {
  scroller = document.body.appendChild(document.createElement("div"));
  scroller.className = "shell-product-body";
  let top = 0, height = 2000;
  Object.defineProperty(scroller, "scrollHeight", { get: () => height });
  Object.defineProperty(scroller, "clientHeight", { get: () => 800 });
  Object.defineProperty(scroller, "scrollTop", { get: () => top, set: (value: number) => { top = value; } });
  root = createRoot(scroller);
  await act(async () => root!.render(<CurationWorkspace workspace={workspace} renderArtifact={() => <div />} />));
  const host = scroller.querySelector<HTMLElement>(".curation-composer-anchor")!;
  const scrollTo = async (value: number) => { await act(async () => { top = value; scroller!.dispatchEvent(new Event("scroll")); }); };

  // Mounting follows the conversation to its end.
  expect(host.dataset.scrollEnd).toBe("true");
  await scrollTo(600);
  expect(host.dataset.scrollEnd).toBe("false");
  await scrollTo(1199.5);
  expect(host.dataset.scrollEnd).toBe("true");
  // A conversation shorter than the window has nothing under the composer either.
  height = 800;
  await scrollTo(0);
  expect(host.dataset.scrollEnd).toBe("true");
});
