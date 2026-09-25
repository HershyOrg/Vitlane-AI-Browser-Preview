// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { useTargetViews } from "./useTargetViews";

let root: Root | undefined;
afterEach(async () => { await act(async () => root?.unmount()); root = undefined; document.body.innerHTML = ""; window.localStorage.clear(); vi.restoreAllMocks(); });

async function mount(curationId: string, now: () => Date) {
  let latest!: ReturnType<typeof useTargetViews>;
  function Probe() { latest = useTargetViews(curationId, now); return null; }
  const el = document.createElement("div"); document.body.append(el); root = createRoot(el);
  await act(async () => root!.render(<Probe />));
  return () => latest;
}

it("remembers the chosen sort and the last opened product per Target on this device", async () => {
  let clock = new Date("2026-09-21T07:00:00Z");
  const views = await mount("curation-1", () => clock);
  await act(async () => views().recordViewed("pens", "pen-3"));
  expect(views().views.pens).toEqual({ signal: { kind: "VIEWED", candidateId: "pen-3", at: "2026-09-21T07:00:00.000Z" } });
  clock = new Date("2026-09-21T07:05:00Z");
  // A sort chosen later replaces the view: its leader is what the reader asked to see.
  await act(async () => views().chooseSort("pens", "PRICE_ASC"));
  expect(views().views.pens).toEqual({ sort: "PRICE_ASC", signal: { kind: "SORT", at: "2026-09-21T07:05:00.000Z" } });
  await act(async () => views().recordViewed("pens", "pen-1"));
  expect(views().views.pens).toMatchObject({ sort: "PRICE_ASC", signal: { kind: "VIEWED", candidateId: "pen-1" } });

  await act(async () => root?.unmount()); root = undefined;
  const again = await mount("curation-1", () => clock);
  expect(again().views.pens).toMatchObject({ sort: "PRICE_ASC", signal: { kind: "VIEWED", candidateId: "pen-1" } });
  await act(async () => root?.unmount()); root = undefined;
  const other = await mount("curation-2", () => clock);
  expect(other().views).toEqual({});
});

it("works for the visit when storage is blocked or holds something else", async () => {
  window.localStorage.setItem("vitlane.curation.target-views.v1:curation-1", "{\"pens\":{\"sort\":7,\"signal\":{\"kind\":\"VIEWED\"}},\"ink\":null}");
  const views = await mount("curation-1", () => new Date("2026-09-21T07:00:00Z"));
  expect(views().views).toEqual({ pens: {} });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
  await act(async () => views().recordViewed("pens", "pen-2"));
  expect(views().views.pens.signal).toMatchObject({ kind: "VIEWED", candidateId: "pen-2" });
});
