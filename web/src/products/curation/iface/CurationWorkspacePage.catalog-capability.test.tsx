// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { CurationWorkspaceResponse } from "../domain/types";
import { WorkspaceArtifact } from "./CurationWorkspacePage";

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

it("renders the canonical Catalog Research surface without a release capability fork", async () => {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    if (String(input).endsWith("/budget")) return Promise.resolve({ok:true,status:200,json:async()=>({schemaVersion:"vitlane.curation-budget.v1",version:0,researchVersion:0,enabled:false,currency:"USD",totalAmount:null,allocations:[{targetId:"target-1",quantity:1,amount:null}]})});
    if (String(input).endsWith("/research-settings") || String(input).endsWith("/exchange-rate")) {
      return Promise.resolve({ ok: true, status: 200, json: async () => String(input).endsWith("/research-settings")
        ? { schemaVersion: "vitlane.research-settings.v1", country: "US", version: 0 }
        : { schemaVersion: "vitlane.exchange-rate.v1", status: "UNAVAILABLE" } } as Response);
    }
    if (String(input).endsWith("/cart")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: async () => ({
          schemaVersion: "vitlane.cart-view.v2",
          curationId: "curation-1", version: 0,
          country: "US", currency: "USD", items: [],
        }),
      } as Response);
    }
    throw new Error(`unexpected request ${String(input)}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const noop = vi.fn(async () => undefined);

  await act(async () => root.render(
    <WorkspaceArtifact
      artifact={{
        id: "artifact-1",
        kind: "CURATION",
        title: "Curation",
        summary: "Retired legacy artifact",
        updatedAt: "2026-08-13T00:00:00Z",
      }}
      response={catalogResearchResponse()}
      working={false}
      catalogCartOpenRequest={0}
      onCatalogCartCountChange={vi.fn()}
      onResearchAgain={noop}
      onAddTargets={noop}
      onStartCurating={noop}
      onRemoveTarget={noop}
    />,
  ));

  expect(container.querySelector("[data-testid='phase8-curation-surface']")).not.toBeNull();
  // A product group is one result in the conversation; its own surface (count, criteria, sort) is a sheet.
  expect(container.textContent).toContain("아직 후보가 없어요");
  await act(async () => container.querySelector<HTMLButtonElement>(".curation-result__more")?.click());
  expect(document.querySelector(".curation-target-sheet")?.textContent).toContain("후보 0/50");
  expect(container.textContent).not.toContain("Retired legacy artifact");
  expect(fetchMock).toHaveBeenCalledTimes(5);
  expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith("/cart"))).toHaveLength(1);
  expect(fetchMock.mock.calls.every(([url]) => /\/(cart|research-settings|exchange-rate|budget|criteria)$/.test(String(url)))).toBe(true);
  await act(async () => root.unmount());
});

function catalogResearchResponse() {
  return {
    curation: { id: "curation-1", phase: "CURATING" },
    plan: {
      locationContext: { country: "US" },
      totalBudget: { amount: "0", currency: "USD" },
    },
    targets: [{
      id: "target-1", title: "Commuter backpack",
      normalizedIntent: "commuter backpack", category: "Bags",
      researchScope: { country: "US" },
    }],
    research: { groups: [] },
    cart: { selections: [] },
    availableActions: [],
    timeline: [],
    latestArtifact: "CURATION_BOARD",
    catalogResearch: {
      schemaVersion: "vitlane.catalog-research-workspace.v1",
      pools: [{
        targetId: "target-1", version: 1, expandOrdinal: 0,
        products: [], hiddenProducts: [],
      }],
      configurations: [], interactions: [],
    },
  } as unknown as CurationWorkspaceResponse;
}
