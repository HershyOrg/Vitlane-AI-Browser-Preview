// @vitest-environment jsdom

import { act, type ComponentProps, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CurationWorkspaceResponse } from "../domain/types";
import type {
  LiveCatalogProviderMessage,
  LiveCatalogProduct,
  CatalogWorkspaceResponse,
} from "../research/infra/liveCatalogReviewApi";
import { CatalogCurationResearch } from "./CatalogCurationResearch";
import {
  reconcileConversationTurn,
  type ConversationDraft,
  type ConversationMessage,
} from "../domain/conversation";
import { CurationConversationBubble } from "./CurationConversationBubble";
import { LocaleProvider } from "../../../shared/i18n";

let surfaceCatalogResearchFixture: CatalogWorkspaceResponse | undefined;

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
  document.cookie = "vt_locale_choice=; Max-Age=0; Path=/";
  surfaceCatalogResearchFixture = undefined;
});

describe("CatalogCurationResearch", () => {
  let container: HTMLDivElement | undefined;
  let root: Root | undefined;

  afterEach(async () => {
    if (root) await act(async () => root?.unmount());
    container?.remove();
    root = undefined;
    container = undefined;
  });

  // A response shows one representative per Target; the Target's own surface — criteria, sort, every
  // candidate — is a sheet in the body (ADR-0086). These tests exercise that surface, so the helper
  // opens the first Target's sheet and hands back the whole document; `folded` keeps the conversation
  // as it first appears for the tests about the representative itself.
  async function mountSurface(overrides: Partial<ComponentProps<typeof CatalogCurationResearch>> = {}, folded = false): Promise<HTMLElement> {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(surface(overrides)));
    if (folded) return container;
    await openSheet();
    return document.body;
  }
  async function openSheet(targetId?: string) {
    const scope = targetId ? `[data-result-target="${targetId}"]` : "[data-result-target]";
    // The right edge of a row ("총 후보 n ›" / "펼치기") or the card's "총 후보 n · 펼치기" opens the list; the product itself opens its details.
    const trigger = document.querySelector<HTMLButtonElement>(`${scope} .curation-result__list, ${scope} .curation-result__more`);
    if (trigger && !document.querySelector(".curation-target-sheet")) await act(async () => trigger.click());
  }

  it("removes Expand and submits explicit Research Again with empty feedback", async () => {
    const fetchMock = installCatalogFetch({ workspace: catalogWorkspacePayload(liveSearchPayload("existing", "Existing product").products) });
    const onResearchAgain = vi.fn().mockResolvedValue(undefined);
    const view = await mountSurface({ onResearchAgain }); await settle();
    expect(view.textContent).not.toContain("후보 더 찾기");
    expect(view.querySelector(".catalog-ui-target__research-again")).toBeNull();
 await selectResearch(view);
    await act(async () => button(view, "조사 요청 보내기").click()); await settle();
    expect(onResearchAgain).toHaveBeenCalledWith("target-1", "");
    expect(callsTo(fetchMock, "/catalog-research/expansions")).toBe(0);
  });

  it("shows dismissible source errors without changing the preserved Candidate or making a command", async () => {
    const workspace = catalogWorkspacePayload(liveSearchPayload("retained", "Retained headphones").products);
    workspace.pools[0].sourceCoverage = [{ source: "AMAZON", status: "FAILED", reasonCode: "PROVIDER_TIMEOUT", candidateCount: 0 }];
    const fetchMock = installCatalogFetch({ workspace });
    const view = await mountSurface(); await settle();
    expect(view.querySelector('.curation-dismissible-notice .vt-notice--danger')?.textContent).toContain("Amazon 조사가 일부 완료되지 않았습니다");
    const count = fetchMock.mock.calls.length;
    await act(async () => button(view, "Amazon 안내 닫기").click()); await settle();
    expect(view.textContent).not.toContain("Amazon 조사가 일부 완료되지 않았습니다");
    expect(view.textContent).toContain("Retained headphones");
    expect(fetchMock.mock.calls.length).toBe(count);
    expect(workspace.pools[0].sourceCoverage[0].status).toBe("FAILED");
  });

  it("keeps unsupported sources and useful partial results out of customer warnings", async () => {
    const workspace = catalogWorkspacePayload(liveSearchPayload("retained", "Retained headphones").products);
    workspace.pools[0].sourceCoverage = [
      { source: "SHOPIFY", status: "UNSUPPORTED", reasonCode: "SHOPIFY_KR_MARKET_UNSUPPORTED", candidateCount: 0 },
      { source: "AMAZON", status: "UNSUPPORTED", reasonCode: "AMAZON_MARKET_UNSUPPORTED", candidateCount: 0 },
      { source: "ELEVENST", status: "PARTIAL", reasonCode: "CATALOG_API_RATE_LIMITED", candidateCount: 2 },
    ];
    installCatalogFetch({ workspace });
    const view = await mountSurface(); await settle();
    expect(view.querySelector(".curation-dismissible-notice")).toBeNull();
    expect(view.textContent).toContain("Retained headphones");
  });

  it("starts with Auto and keeps a selected mode when the selector closes", async () => {
    const view = await mountSurface({}, true);

    expect(view.querySelectorAll(".catalog-ui-focus-composer")).toHaveLength(1);
    expect(view.querySelector('.catalog-ui-focus-composer__context [aria-label="조사·보기 설정"]')).not.toBeNull();
    expect(view.querySelector(".curation-research-settings")).toBeNull();
    // The conversation holds one result per product group; the group's own surface is a sheet, closed at first.
    expect(view.querySelector(".catalog-ui-target")).toBeNull();
    expect(document.querySelector(".curation-target-sheet")).toBeNull();
    expect(view.querySelector('[data-result-target="target-1"] .curation-result__caption')?.textContent).toContain("Commuter backpack");
    expect(button(view, "조사 방식: Auto. 다른 방식 선택").textContent).toContain("Auto");
    expect(view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')?.disabled).toBe(false);

    await act(async () => button(view, "조사 방식: Auto. 다른 방식 선택").click());
    await settle();
    expect(document.body.textContent).toContain("요청을 해석해 상품을 추가하거나 다시 조사합니다.");
    expect(document.body.textContent).not.toContain("재조사 · Commuter backpack");
 await act(async()=>modeOption("재조사").click());
 expect(document.body.textContent).toContain("재조사 · Commuter backpack");

    await act(async () => button(document.body, "닫기").click());
    expect(button(view, "조사 방식: Auto. 다른 방식 선택")).not.toBeNull();
    expect(view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')?.disabled).toBe(false);
  });

  it("lists a product group's candidates as a grid in its sheet, and closing the sheet changes nothing", async () => {
    const products = [
      liveSearchPayload("rail-one", "Rail One").products[0],
      liveSearchPayload("rail-two", "Rail Two").products[0],
      liveSearchPayload("rail-three", "Rail Three").products[0],
    ];
    installCatalogFetch({ workspace: catalogWorkspacePayload(products) });
    const single = await mountSurface();
    await settle();

    const sheet = () => document.querySelector<HTMLElement>(".curation-target-sheet");
    expect(sheet()?.getAttribute("role")).toBe("dialog");
    expect(sheet()?.querySelector("h2")?.textContent).toBe("Commuter backpack");
    expect(sheet()?.textContent).toContain("후보 3/50");
    // A grid that flows down: no sideways rail, no arrows, no tabs for a single product group.
    expect(sheet()?.querySelector(".catalog-ui-candidate-grid")?.getAttribute("role")).toBe("list");
    expect(sheet()?.querySelectorAll(".catalog-ui-candidate-cell")).toHaveLength(3);
    expect(single.querySelector('[data-horizontal-scroll="true"]')).toBeNull();
    expect(sheet()?.querySelector('[role="tablist"]')).toBeNull();
    // One product group in the response: a card, and the conversation itself never holds the board.
    expect(container?.querySelector('[data-result-target="target-1"]')?.classList).toContain("curation-result--card");
    expect(container?.querySelector(".catalog-ui-target")).toBeNull();

    await act(async () => button(sheet()!, "닫기").click());
    expect(sheet()).toBeNull();
    expect(container?.textContent).toContain("Commuter backpack");
    // The whole card is the target: a click on its background, or on a number, opens the product — not only the name.
    const card = container!.querySelector<HTMLElement>('[data-result-target="target-1"] .curation-result__card')!;
    const candidateModal = () => document.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]');
    for (const spot of [card, card.querySelector<HTMLElement>(".curation-result__numbers")!]) {
      await act(async () => spot.click());
      await settle();
      expect(candidateModal()?.querySelector("h2")?.textContent).toBe(card.querySelector(".curation-result__title")?.textContent);
      expect(sheet()).toBeNull();
      await act(async () => button(candidateModal()!, "상품 상세 닫기").click());
      expect(candidateModal()).toBeNull();
    }
    // The name button still opens it once (its own click does not also count as the card's).
    await act(async () => card.querySelector<HTMLButtonElement>(".curation-result__title")!.click());
    await settle();
    expect(document.querySelectorAll('.catalog-ui-candidate-modal[role="dialog"]')).toHaveLength(1);
    await act(async () => button(candidateModal()!, "상품 상세 닫기").click());
    await act(async () => container?.querySelector<HTMLButtonElement>('[data-result-target="target-1"] .curation-result__more')?.click());
    expect(sheet()?.textContent).toContain("Rail Three");

    await act(async () => root?.unmount());
    root = undefined;
    container?.remove();
    container = undefined;

    const response = workspaceResponse();
    const secondTarget = {
      ...response.targets[0],
      id: "target-2",
      title: "Trail shoes",
      normalizedIntent: "trail shoes",
      orderIndex: 1,
    };
    response.targets = [...response.targets, secondTarget];
    response.research.groups = [
      ...response.research.groups,
      {
        ...response.research.groups[0],
        session: {
          ...response.research.groups[0].session,
          id: "session-2",
          planTargetId: "target-2",
        },
      },
    ];
    const multiWorkspace = catalogWorkspacePayload(products);
    multiWorkspace.pools.push({
      ...multiWorkspace.pools[0],
      targetId: "target-2",
      products: [liveSearchPayload("rail-four", "Rail Four").products[0]],
    });
    installCatalogFetch({ workspace: multiWorkspace });
    await mountSurface({ response });
    await settle();

    // Several product groups: one row each, and the sheet moves between them by tab.
    expect(Array.from(container!.querySelectorAll("[data-result-target]")).map(row => row.className)).toEqual(["curation-result curation-result--row", "curation-result curation-result--row"]);
    // The way into a group's list is a stack: the next two candidates peek out and the number counts what the row hides.
    const stacks = Array.from(container!.querySelectorAll<HTMLElement>("[data-result-target] .curation-result__list"));
    expect(stacks.map(list => [list.querySelectorAll(".curation-result__stack-item").length, list.querySelector(".curation-result__list-count")?.textContent ?? ""])).toEqual([[2, "+2"], [0, ""]]);
    // A row carries no score and no coloured mall name: the mall is its logo right after the product's name (owner 2026-09-23).
    for (const row of Array.from(container!.querySelectorAll<HTMLElement>("[data-result-target] .curation-result__row"))) {
      expect(row.querySelector(".candidate-comparison__score, .curation-result__unscored, .candidate-source-badge")).toBeNull();
      expect(row.querySelector(".curation-result__name-line > .curation-result__name + .curation-mall-logo")?.getAttribute("alt")).toBe("Shopify");
    }
    const tabs = Array.from(sheet()!.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
    // The tab row is the index of every candidate: "All" first, then one tab per product group.
    expect(tabs.map(tab => [tab.textContent, tab.getAttribute("aria-selected")])).toEqual([["전체4", "false"], ["Commuter backpack3", "true"], ["Trail shoes1", "false"]]);
    expect(sheet()?.textContent).not.toContain("Rail Four");
    // "All": every group's candidates under a heading per group, without the groups' own tools.
    await act(async () => tabs[0].click());
    await settle();
    expect(sheet()?.getAttribute("data-target-sheet")).toBe("*all");
    expect(Array.from(sheet()!.querySelectorAll(".curation-target-sheet__group-title")).map(node => node.textContent)).toEqual(["Commuter backpack", "Trail shoes"]);
    expect(sheet()!.querySelectorAll(".vt-candidate-card")).toHaveLength(4);
    expect(sheet()!.querySelector(".research-sort__trigger, .curation-target-sheet__remove, .research-criteria")).toBeNull();
    // A group's heading goes to that group's own tab.
    await act(async () => sheet()!.querySelectorAll<HTMLButtonElement>(".curation-target-sheet__group-title")[1].click());
    await settle();
    expect(sheet()?.querySelector("h2")?.textContent).toBe("Trail shoes");
    await act(async () => Array.from(sheet()!.querySelectorAll<HTMLButtonElement>('[role="tab"]'))[1].click());
    await settle();
    await act(async () => Array.from(sheet()!.querySelectorAll<HTMLButtonElement>('[role="tab"]'))[2].click());
    await settle();
    expect(sheet()?.querySelector("h2")?.textContent).toBe("Trail shoes");
    expect(sheet()?.textContent).toContain("Rail Four");
    expect(sheet()?.querySelectorAll(".catalog-ui-candidate-cell")).toHaveLength(1);
  });

  it("lets the reader change the product a response shows: the last one opened, the leader of a chosen sort, a carted one above both", async () => {
    const scored = (id: string, title: string, totalScore: number, priceMinor: number) => {
      const product = liveSearchPayload(id, title).products[0];
      // Prices in the display currency, so the price sorts need no exchange rate.
      return { ...product, currency: "KRW", priceMinimumMinor: priceMinor, priceMaximumMinor: priceMinor, previewVariant: { ...product.previewVariant!, priceMinor, currency: "KRW" },
        axisAssessment: { totalScore, scores: [], weights: [], criteria: { axes: [] } } as unknown as LiveCatalogProduct["axisAssessment"] };
    };
    installCatalogFetch({ workspace: catalogWorkspacePayload([scored("best", "Best Pack", 90, 12000), scored("cheap", "Cheap Pack", 70, 4000), scored("third", "Third Pack", 60, 9000)]), preparation: preparationPayload() });
    await mountSurface({}, true);
    await settle();
    const result = () => container!.querySelector<HTMLElement>('[data-result-target="target-1"]')!;
    const shown = () => [result().getAttribute("data-representative"), result().querySelector(".curation-result__why")?.textContent];
    expect(shown()).toEqual(["best", "Pick"]);

    // Opening a product's details from the sheet makes it the one the conversation shows, on this device.
    await openSheet();
    await act(async () => button(document.body, "Third Pack 옵션 보기").click());
    await settle();
    // Two layers deep: the product group's sheet stays mounted behind the product's details.
    expect(document.querySelector(".curation-target-sheet")).not.toBeNull();
    expect(document.querySelector(".catalog-ui-candidate-modal")).not.toBeNull();
    await act(async () => button(document.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!, "상품 상세 닫기").click());
    expect(document.querySelector(".catalog-ui-candidate-modal")).toBeNull();
    expect(document.querySelector(".curation-target-sheet")).not.toBeNull();
    expect(shown()).toEqual(["third", "마지막으로 본 상품"]);
    expect(window.localStorage.getItem("vitlane.curation.target-views.v1:curation-1")).toContain('"candidateId":"third"');

    // A sort chosen afterwards is the newer statement: its leader takes over and the caption says which sort.
    const trigger = document.querySelector<HTMLButtonElement>(".curation-target-sheet .research-sort__trigger")!;
    await act(async () => { trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); });
    const option = (label: string) => Array.from(document.querySelectorAll<HTMLElement>("[role='menuitemradio']")).find(item => item.textContent === label)!;
    await act(async () => option("낮은 가격순").click());
    expect(shown()).toEqual(["cheap", "최저가"]);
    expect(Array.from(document.querySelectorAll(".curation-target-sheet .vt-candidate-card__title")).map(title => title.textContent)).toEqual(["Cheap Pack", "Third Pack", "Best Pack"]);
    // Choosing Vitlane Pick again returns to the Pick.
    await act(async () => { trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })); });
    await act(async () => option("Vitlane Pick").click());
    expect(shown()).toEqual(["best", "Pick"]);

    // A carted product stays in front of whatever the reader looks at next.
    await act(async () => button(document.body, "Cheap Pack 옵션 보기").click());
    await settle();
    await act(async () => button(document.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!, "장바구니 담기").click());
    await settle();
    // Adding to the cart closes the product's details and returns to the sheet.
    expect(document.querySelector(".catalog-ui-candidate-modal")).toBeNull();
    expect(document.querySelector(".curation-target-sheet")).not.toBeNull();
    await act(async () => button(document.body, "Third Pack 옵션 보기").click());
    await settle();
    await act(async () => button(document.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!, "상품 상세 닫기").click());
    expect(shown()).toEqual(["cheap", "담은 상품"]);
  });

  it("opens the product from the row and the list from the row's right edge, and keeps no all-candidates entry in the composer", async () => {
    const response = workspaceResponse();
    response.targets = [...response.targets, { ...response.targets[0], id: "target-2", title: "Trail shoes", normalizedIntent: "trail shoes", orderIndex: 1 }];
    response.research.groups = [...response.research.groups, { ...response.research.groups[0], session: { ...response.research.groups[0].session, id: "session-2", planTargetId: "target-2" } }];
    const workspace = catalogWorkspacePayload(liveSearchPayload("pack-one", "Pack One").products);
    workspace.pools.push({ ...workspace.pools[0], targetId: "target-2", products: liveSearchPayload("shoe-one", "Shoe One").products });
    installCatalogFetch({ workspace, preparation: preparationPayload() });
    await mountSurface({ response }, true);
    await settle();

    const row = container!.querySelector<HTMLElement>('[data-result-target="target-1"]')!;
    const product = row.querySelector<HTMLButtonElement>(".curation-result__row")!;
    const list = row.querySelector<HTMLButtonElement>(".curation-result__list")!;
    expect(product.getAttribute("aria-label")).toBe("Commuter backpack: Pack One 상세 보기");
    // The only candidate is the one the row shows, so nothing peeks out from behind it; the name still says what opens.
    expect(list.querySelector(".curation-result__list-count")).toBeNull();
    expect(list.querySelector(".curation-result__stack")).toBeNull();
    expect(list.getAttribute("aria-label")).toBe("Commuter backpack 후보 1개 펼치기");
    // The composer keeps the cart only. Every candidate at once is under the last row, on the right.
    expect(container!.querySelector(".catalog-ui-composer-dock")?.textContent ?? "").not.toContain("전체 후보");
    const all = container!.querySelector<HTMLButtonElement>(".curation-results__all")!;
    expect(all.textContent).toBe("전체 후보 보기");
    const combination = container!.querySelector<HTMLButtonElement>(".curation-results__combination")!;
    expect(combination.textContent).toBe("조합을 Cart에 추가");
    expect(combination.nextElementSibling).toBe(all);
    expect(combination.closest(".curation-results")).not.toBeNull();
    expect(container!.querySelector(".catalog-ui-composer-dock")?.textContent ?? "").not.toContain("조합 추천");
    expect(container!.querySelector(".combination-response, .combination-review")).toBeNull();

    // The product: straight into its details, without the product group's list.
    await act(async () => product.click());
    await settle();
    expect(document.querySelector(".catalog-ui-candidate-modal h2")?.textContent).toBe("Pack One");
    expect(document.querySelector(".curation-target-sheet")).toBeNull();
    await act(async () => button(document.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!, "상품 상세 닫기").click());

    // The right edge: the product group's list.
    await act(async () => list.click());
    expect(document.querySelector(".curation-target-sheet")?.textContent).toContain("Pack One");
    expect(document.querySelector(".catalog-ui-candidate-modal")).toBeNull();
    // Neither sheet paints a scrim.
    expect(document.querySelector(".curation-target-sheet")?.parentElement?.classList.contains("vt-scrim")).toBe(false);
    await act(async () => button(document.querySelector<HTMLElement>(".curation-target-sheet")!, "닫기").click());

    // "View all candidates": the same sheet on its first tab.
    await act(async () => all.click());
    await settle();
    const tabs = Array.from(document.querySelectorAll<HTMLButtonElement>('.curation-target-sheet [role="tab"]'));
    expect(tabs.map(tab => [tab.textContent, tab.getAttribute("aria-selected")])).toEqual([["전체2", "true"], ["Commuter backpack1", "false"], ["Trail shoes1", "false"]]);
    expect(document.querySelector(".curation-target-sheet")?.textContent).toContain("Pack One");
    expect(document.querySelector(".curation-target-sheet")?.textContent).toContain("Shoe One");
  });

  it("takes a product group out from the end of its sheet, behind a confirmation that closes first", async () => {
    installCatalogFetch({ workspace: catalogWorkspacePayload(liveSearchPayload("keep", "Keep Pack").products) });
    const onRemoveTarget = vi.fn().mockResolvedValue(undefined);
    await mountSurface({ onRemoveTarget });
    await settle();
    // The conversation has no remove control; the sheet ends with one, in words.
    expect(container!.querySelector('[aria-label="Commuter backpack 상품 제거"]')).toBeNull();
    const remove = document.querySelector<HTMLButtonElement>(".curation-target-sheet__foot .curation-target-sheet__remove")!;
    expect(remove.textContent).toBe("상품군 빼기");
    await act(async () => remove.click());
    const confirm = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    // The confirmation is its own layer in the body, so the sheet's inert page cannot swallow it.
    expect(confirm.closest("body > .catalog-ui-confirm-backdrop")).not.toBeNull();
    await act(async () => button(confirm, "취소").click());
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(document.querySelector(".curation-target-sheet")).not.toBeNull();
    await act(async () => remove.click());
    await act(async () => button(document.querySelector<HTMLElement>('[role="alertdialog"]')!, "제거").click());
    await settle();
    expect(onRemoveTarget).toHaveBeenCalledWith("target-1");
  });

  it("preserves the draft while closing or changing the research mode", async () => {
    const view = await mountSurface({}, true);
    const textarea = view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')!;
    await act(async () => setTextareaValue(textarea, "초안은 그대로 유지해 줘"));

    await act(async () => button(view, "조사 방식: Auto. 다른 방식 선택").click());
    await settle();
    await act(async () => button(document.body, "닫기").click());
    expect(textarea.value).toBe("초안은 그대로 유지해 줘");
    expect(textarea.disabled).toBe(false);

    await act(async () => button(view, "조사 방식: Auto. 다른 방식 선택").click());
    await settle();
    const addTargetOption = Array.from(
      document.body.querySelectorAll<HTMLButtonElement>(".catalog-ui-mode-selector__option"),
    ).find((candidate) => candidate.querySelector("strong")?.textContent === "상품 추가");
    expect(addTargetOption).not.toBeUndefined();
    await act(async () => addTargetOption?.click());
    expect(textarea.value).toBe("초안은 그대로 유지해 줘");
    expect(textarea.disabled).toBe(false);
    expect(button(view, "조사 방식: 상품 추가. 다른 방식 선택")).not.toBeNull();
  });

  it("sends Auto refinement to the server-owned resolver and applies its Research again result", async () => {
    const onAddTargets = vi.fn();
    const onResearchAgain = vi.fn();
    const fetchMock = vi.fn((input: RequestInfo | URL, _init?: RequestInit) => {
      if (String(input).includes("/conversation-requests")) {
        return Promise.resolve(jsonResponse({
          status: "EXECUTED",
          decision: "RESEARCH_AGAIN",
          source: "MANAGED",
          targetId: "target-1",
          reasonCode: "MANAGED_NORMALIZED_TARGET_MATCH",
          replay: false,
        }));
      }
      return Promise.reject(new Error(`unexpected fetch ${input}`));
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mountSurface({ onAddTargets, onResearchAgain });
    const textarea = view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')!;

    await act(async () => setTextareaValue(textarea, "더 저렴한 방수 옵션을 찾아줘"));
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    await settle();

    const autoCall = fetchMock.mock.calls.find(([input]) => String(input).includes("/conversation-requests"));
    expect(autoCall).not.toBeUndefined();
    expect(JSON.parse(String(autoCall?.[1]?.body))).toMatchObject({
      request: "더 저렴한 방수 옵션을 찾아줘",
      expectedCurationVersion: 2,
    });
    expect(String(autoCall?.[1]?.headers && (autoCall[1].headers as Record<string, string>)["Idempotency-Key"])).not.toBe("");
    expect(onResearchAgain).not.toHaveBeenCalled();
    expect(onAddTargets).not.toHaveBeenCalled();
    expect(button(view, "조사 방식: Auto. 다른 방식 선택")).not.toBeNull();
    expect(textarea.value).toBe("");
    expect(view.textContent).toContain("요청하신 내용으로 Commuter backpack을 다시 조사합니다.");
    const liveSlot = view.querySelector('[data-testid="phase8-latest-slot"]')!;
    expect(liveSlot.textContent).toContain("더 저렴한 방수 옵션을 찾아줘");
    expect(Array.from(liveSlot.querySelectorAll(".curation-conversation-bubble")).map(
      (bubble) => bubble.classList.contains("is-user") ? "USER" : "VITLANE",
    )).toEqual(["USER", "VITLANE"]);
    expect(
      view.querySelector('[data-testid="phase8-conversation-history"]')
        ?.querySelectorAll(".curation-conversation-bubble"),
    ).toHaveLength(0);
  });

  it("disables the composer for the full Auto resolver request", async () => {
    const pendingAuto = deferred<Response>();
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/conversation-requests")) return pendingAuto.promise;
      return Promise.reject(new Error(`unexpected fetch ${input}`));
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mountSurface();
    const textarea = view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')!;
    const form = textarea.form!;

    await act(async () => setTextareaValue(textarea, "처리 중에는 다시 보내지 마"));
    await act(async () => {
      button(view, "조사 요청 보내기").click();
      await Promise.resolve();
    });

    expect(view.textContent).toContain("Auto 요청 해석 중");
    expect(textarea.disabled).toBe(true);
    expect(button(view, "조사 요청 보내기").disabled).toBe(true);
    expect(button(view, "조사 방식: Auto. 다른 방식 선택").disabled).toBe(true);

    await act(async () => {
      form.requestSubmit();
      await Promise.resolve();
    });
    expect(callsTo(fetchMock, "/conversation-requests", "POST")).toBe(1);

    await act(async () => {
      pendingAuto.resolve(jsonResponse({
        status: "EXECUTED",
        decision: "RESEARCH_AGAIN",
        source: "MANAGED",
        targetId: "target-1",
        reasonCode: "MANAGED_NORMALIZED_TARGET_MATCH",
        replay: false,
      }));
      await pendingAuto.promise;
    });
    await settle();

    expect(textarea.disabled).toBe(false);
    expect(button(view, "조사 방식: Auto. 다른 방식 선택").disabled).toBe(false);
  });

  it("uses the server result for a new Target instead of invoking a browser classifier", async () => {
    const onAddTargets = vi.fn();
    const onResearchAgain = vi.fn();
    const fetchMock = vi.fn((input: RequestInfo | URL, _init?: RequestInit) => {
      if (String(input).includes("/conversation-requests")) {
        return Promise.resolve(jsonResponse({
          status: "EXECUTED",
          decision: "ADD_TARGET",
          source: "MANAGED",
          reasonCode: "NEW_PRODUCT_REFERENCE",
          replay: false,
        }));
      }
      return Promise.reject(new Error(`unexpected fetch ${input}`));
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mountSurface({ onAddTargets, onResearchAgain });
    const textarea = view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')!;

    await act(async () => setTextareaValue(textarea, "여행용 캐리어도 찾아줘"));
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    await settle();

    expect(callsTo(fetchMock, "/conversation-requests", "POST")).toBe(1);
    expect(onAddTargets).not.toHaveBeenCalled();
    expect(onResearchAgain).not.toHaveBeenCalled();
    expect(textarea.value).toBe("");
    expect(view.textContent).toContain("새 상품을 조사 목록에 추가합니다.");
  });

  it("preserves ambiguous Auto input and opens the selector instead of dispatching", async () => {
    const response = workspaceResponse();
    response.targets = [
      ...response.targets,
      {
        ...response.targets[0],
        id: "target-2",
        title: "Trail shoes",
        normalizedIntent: "trail shoes",
        orderIndex: 1,
      },
    ];
    const onAddTargets = vi.fn();
    const onResearchAgain = vi.fn();
    const fetchMock = vi.fn((input: RequestInfo | URL, _init?: RequestInit) => {
      if (String(input).includes("/conversation-requests")) {
        return Promise.resolve(jsonResponse({
          status: "NEEDS_SELECTION",
          decision: "NEEDS_SELECTION",
          source: "MANAGED",
          reasonCode: "TARGET_REQUIRED",
          replay: false,
        }));
      }
      return Promise.reject(new Error(`unexpected fetch ${input}`));
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mountSurface({ response, onAddTargets, onResearchAgain });
    const textarea = view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')!;

    await act(async () => setTextareaValue(textarea, "가격을 더 낮춰서 찾아줘"));
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    await settle();

    expect(onAddTargets).not.toHaveBeenCalled();
    expect(onResearchAgain).not.toHaveBeenCalled();
    expect(textarea.value).toBe("가격을 더 낮춰서 찾아줘");
    expect(callsTo(fetchMock, "/conversation-requests", "POST")).toBe(1);
    expect(document.body.textContent).toContain("다시 조사할 상품을 선택하거나 상품 추가를 선택해 주세요.");
    await act(async()=>modeOption("재조사").click());
 expect(document.body.textContent).toContain("재조사 · Trail shoes");
  });

  it("hydrates Shopify and Amazon Candidates once through the workspace and retains both sources", async () => {
    const shop = liveSearchPayload("shop-candidate", "Shared Shopify candidate").products[0];
    const ref = { source: "AMAZON", marketplace: "US", asin: "B012345678" } as const;
    const amazon: LiveCatalogProduct = { ...shop, candidateId: "amazon-candidate", source: "AMAZON", title: "Shared Amazon candidate", sourceProductRef: { source: "AMAZON", marketplace: "US", anchorAsin: ref.asin },
      variantObservation: { observationId: "observation", variantRef: ref, price: { kind: "OBSERVED", amountMinor: 7900, currency: "USD" }, availability: "UNKNOWN", deliveryEligibility: "UNCONFIRMED", seller: { kind: "UNKNOWN" }, purchaseRoute: "EXTERNAL", productUrl: "https://www.amazon.com/dp/B012345678", observedAt: "2026-09-10T00:00:00Z", refreshAfter: "2026-09-10T00:15:00Z" } };
    surfaceCatalogResearchFixture = storedWorkspacePayload(catalogWorkspacePayload([shop, amazon])) as CatalogWorkspaceResponse;
    surfaceCatalogResearchFixture.pools[0].products[1].variantObservation = undefined;
    const hydrationSources: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/cart")) return jsonResponse(cartPayload(0, []));
      if (path.endsWith("/state")) return jsonResponse({variantRef: ref, configurationVersion: 0, purchaseFeedback: {version: 0, records: []}});
      if (path.endsWith("/hydrations")) {
        const body = JSON.parse(init?.body as string); hydrationSources.push(body.source ?? "SHOPIFY");
        if (body.source === "AMAZON") { expect(body.candidateId).toBe("amazon-candidate"); return jsonResponse(catalogWorkspacePayload([amazon])); }
        return jsonResponse(catalogWorkspacePayload([shop]));
      }
      throw new Error(`Unexpected API ${path}`);
    }));
    HTMLDialogElement.prototype.close = vi.fn(); HTMLDialogElement.prototype.showModal = vi.fn();
    const view = await mountSurface(); await act(async () => { await new Promise(resolve => setTimeout(resolve, 1100)); });
    expect(hydrationSources.sort()).toEqual(["AMAZON", "SHOPIFY"]);
    expect(view.textContent).toContain("Shared Shopify candidate");
    expect(view.textContent).toContain("Shared Amazon candidate");
    expect(view.querySelectorAll(".vt-candidate-card")).toHaveLength(2);
    expect(view.querySelectorAll(".vt-candidate-card.is-loading")).toHaveLength(0);
  });

  it("renders durable Candidate skeletons before Shopify enrichment or CartView hydration settles", async () => {
    const durable = liveSearchPayload(
      "candidate-durable-12345678",
      "Provider title must not be stored",
    );
    durable.products[0].intentPoint = "Durable intent point from Candidate assessment";
    durable.products[0].features = ["Durable feature"];
    durable.products[0].specifications = ["Durable spec"];
    const workspace = catalogWorkspacePayload(durable.products);
    surfaceCatalogResearchFixture = storedWorkspacePayload(workspace) as CatalogWorkspaceResponse;
    const pendingSignals: AbortSignal[] = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/catalog-research/hydrations") || path.endsWith("/cart")) {
        pendingSignals.push(init?.signal as AbortSignal);
        return pendingUntilAbort(init?.signal);
      }
      throw new Error(`unexpected fetch ${init?.method ?? "GET"} ${path}`);
    }));

    const view = await mountSurface();
    await settle();

    const skeleton = view.querySelector<HTMLElement>(".vt-candidate-card.is-loading");
    expect(skeleton).not.toBeNull();
    expect(skeleton?.textContent).toContain("추천 상품");
    expect(skeleton?.querySelector(".vt-candidate-card__evidence")?.textContent).toBe("Durable intent point from Candidate assessment");
    expect(skeleton?.textContent).toContain("가격 확인 중");
    expect(view.textContent).toContain("상품 정보를 불러오는 중");
    expect(button(view, "장바구니 …").disabled).toBe(true);
    expect(pendingSignals).toHaveLength(2);
    // The representative in the conversation is the same unread product: it is being read, not "price unknown",
    // and it never shows a blank name or a zero.
    const representative = container!.querySelector<HTMLElement>("[data-result-target]")!;
    expect(representative.textContent).toContain("상품 정보를 불러오는 중");
    expect(representative.querySelector(".curation-result__price")?.textContent).toBe("가격 확인 중");
    expect(representative.textContent).not.toContain("가격 미확인");
    expect(representative.textContent).not.toMatch(/(^|\D)0\$/);
  });

  it("shows the chosen option's price on the representative, the same price its card shows", async () => {
    const current = liveSearchPayload("saved-option", "Saved Option Pack");
    const variants = variantPagePayload();
    const workspace = {
      ...catalogWorkspacePayload(current.products),
      configurations: [{ candidateId: "saved-option", variant: variants.rows[1], observedAt: variants.observedAt }],
    };
    installCatalogFetch({ workspace, variantPages: [variants] });
    const view = await mountSurface();
    await settle();

    const optionPrice = view.querySelector<HTMLElement>(".vt-candidate-card .vt-candidate-card__price")?.textContent ?? "";
    expect(optionPrice).toContain("84$");
    const representative = container!.querySelector<HTMLElement>("[data-result-target] .curation-result__price strong")!;
    expect(representative.textContent).toContain("84$");
  });

  it("normalizes invalid null Candidate claim lists during a polling merge", async () => {
    const product = liveSearchPayload(
      "candidate-null-claims",
      "Polling Safe Pack",
    ).products[0];
    product.features = ["Fresh feature"];
    product.specifications = ["Fresh specification"];
    const initialWorkspace = catalogWorkspacePayload([product]);
    installCatalogFetch({ workspace: initialWorkspace });
    const initialResponse = workspaceResponse();
    initialResponse.catalogResearch = storedWorkspacePayload(
      initialWorkspace,
    ) as unknown as CurationWorkspaceResponse["catalogResearch"];
    const view = await mountSurface({ response: initialResponse });
    await settle();
    expect(view.textContent).toContain("Polling Safe Pack");

    const nextWorkspace = catalogWorkspacePayload([product]);
    nextWorkspace.pools[0].version = 2;
    const invalidWireWorkspace = storedWorkspacePayload(nextWorkspace) as {
      pools: Array<{
        products: Array<{ features: string[] | null; specifications: string[] | null }>;
      }>;
    };
    invalidWireWorkspace.pools[0].products[0].features = null;
    invalidWireWorkspace.pools[0].products[0].specifications = null;
    const nextResponse = workspaceResponse();
    nextResponse.catalogResearch = invalidWireWorkspace as unknown as
      CurationWorkspaceResponse["catalogResearch"];

    await act(async () => root?.render(surface({ response: nextResponse })));
    await settle();

    expect(view.textContent).toContain("Polling Safe Pack");
    expect(view.textContent).not.toContain("화면을 표시하지 못했습니다");
  });

  it("preserves a failed Target skeleton and retries hydration explicitly", async () => {
    const durable = liveSearchPayload("candidate-retry", "Stored title");
    const fresh = liveSearchPayload("candidate-retry", "Fresh retry title");
    surfaceCatalogResearchFixture = storedWorkspacePayload(
      catalogWorkspacePayload(durable.products),
    ) as CatalogWorkspaceResponse;
    let hydrationCalls = 0;
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = String(input);
      if (path.includes("/catalog-research/hydrations")) {
        hydrationCalls += 1;
        return Promise.resolve(hydrationCalls === 1
          ? errorResponse({
              code: "PROVIDER_UNAVAILABLE",
              reasonCode: "UNAVAILABLE",
              retryable: true,
            })
          : jsonResponse(catalogWorkspacePayload(fresh.products)));
      }
      if (path.endsWith("/cart")) return Promise.resolve(jsonResponse(cartPayload(0, [])));
      throw new Error(`unexpected fetch ${path}`);
    }));

    const view = await mountSurface();
    await settle();
    expect(view.textContent).toContain("저장된 추천 상품은 그대로입니다");
    expect(view.textContent).not.toContain("UNAVAILABLE");
    expect(view.querySelectorAll(".vt-candidate-card.is-loading")).toHaveLength(0);
    expect(view.querySelectorAll(".vt-candidate-card.is-unavailable")).toHaveLength(1);
    expect(view.textContent).toContain("불러오기가 중단되었습니다");

    await act(async () => button(view, "상품 정보 다시 불러오기").click());
    await settle();
    expect(hydrationCalls).toBe(2);
    expect(view.textContent).toContain("Fresh retry title");
  });

  it("finishes 11 of 12 candidates, marks one unresolved, and retries only that candidate", async () => {
    const products = Array.from({ length: 12 }, (_, index) =>
      liveSearchPayload(`candidate-${index + 1}`, `Ready product ${index + 1}`).products[0]
    );
    const unresolved = storedCandidateProduct(products[11]);
    unresolved.hydration = {
      status: "UNRESOLVED",
      reasonCode: "CATALOG_RESEARCH_CANDIDATE_UNRESOLVED",
      retryable: true,
    };
    const partialWorkspace = catalogWorkspacePayload([
      ...products.slice(0, 11),
      unresolved,
    ]);
    partialWorkspace.schemaVersion = "vitlane.catalog-research-hydration.v2";
    const recoveredWorkspace = catalogWorkspacePayload([
      ...products.slice(0, 11),
      liveSearchPayload("candidate-12", "Recovered product 12").products[0],
    ]);
    recoveredWorkspace.schemaVersion = "vitlane.catalog-research-hydration.v2";
    const fetchMock = installCatalogFetch({
      workspaceSequence: [partialWorkspace, partialWorkspace, recoveredWorkspace],
    });

    const view = await mountSurface();
    await settle();

    expect(view.querySelectorAll(".vt-candidate-card.is-loading")).toHaveLength(0);
    expect(view.querySelectorAll(".vt-candidate-card.is-unavailable")).toHaveLength(1);
    expect(view.textContent).toContain("Ready product 11");
    expect(view.textContent).toContain("이 상품 조회는 상세 정보 없이 완료되었습니다.");
    expect(view.textContent).not.toContain("상품 정보를 불러오는 중");

    window.localStorage.setItem("vitlane.locale.v1", "en-US");
    document.cookie = "vt_locale_choice=en-US; Path=/";
    await act(async () => root?.render(
      <LocaleProvider>{surface()}</LocaleProvider>,
    ));
    await settle();
    // A new provider remounts the surface, and a remounted result starts folded.
    await openSheet();
    expect(view.textContent).toContain("This product lookup finished without details.");
    expect(view.textContent).toContain("Shopify could not resolve this saved product.");

    const unresolvedEntry = view.querySelector<HTMLElement>(
      '[data-candidate-id="candidate-12"]',
    );
    if (!unresolvedEntry) throw new Error("unresolved candidate entry missing");
    await act(async () => button(unresolvedEntry, "Try this product again").click());
    await settle();

    expect(view.textContent).toContain("Recovered product 12");
    expect(view.querySelectorAll(".vt-candidate-card.is-unavailable")).toHaveLength(0);
    const hydrationBodies = fetchMock.mock.calls
      .filter(([input]) => String(input).includes("/catalog-research/hydrations"))
      .map(([, init]) => JSON.parse(String(init?.body)));
    expect(hydrationBodies).toHaveLength(3);
    expect(hydrationBodies[2]).toEqual({
      scope: "CANDIDATE",
      targetId: "target-1",
      candidateId: "candidate-12",
    });
    document.cookie = "vt_locale_choice=; Max-Age=0; Path=/";
  });

  it("hydrates all 52 stored Candidates with at most five Target lookups in flight", async () => {
    const response = workspaceResponse();
    const counts = [8, 7, 7, 6, 6, 6, 6, 6];
    response.targets = counts.map((_, index) => ({
      ...response.targets[0],
      id: `target-${index + 1}`,
      title: `Target ${index + 1}`,
      orderIndex: index,
    }));
    const pools = counts.map((count, targetIndex) => ({
      ...catalogWorkspacePayload([]).pools[0],
      targetId: `target-${targetIndex + 1}`,
      products: Array.from({ length: count }, (_, candidateIndex) =>
        liveSearchPayload(
          `candidate-${targetIndex + 1}-${candidateIndex + 1}`,
          `Candidate ${targetIndex + 1}-${candidateIndex + 1}`,
        ).products[0]),
    }));
    const workspace = {
      ...catalogWorkspacePayload([]),
      pools,
    };
    surfaceCatalogResearchFixture = storedWorkspacePayload(workspace) as CatalogWorkspaceResponse;

    let activeHydrations = 0;
    let maximumActiveHydrations = 0;
    const hydrationTargetIds: string[] = [];
    const pendingHydrations = new Map<string, ReturnType<typeof deferred<Response>>>();
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/catalog-research/hydrations")) {
        const targetId = JSON.parse(String(init?.body)).targetId as string;
        const pending = deferred<Response>();
        hydrationTargetIds.push(targetId);
        pendingHydrations.set(targetId, pending);
        activeHydrations += 1;
        maximumActiveHydrations = Math.max(maximumActiveHydrations, activeHydrations);
        return pending.promise.finally(() => {
          activeHydrations -= 1;
        });
      }
      if (path.endsWith("/cart")) return Promise.resolve(jsonResponse(cartPayload(0, [])));
      throw new Error(`unexpected fetch ${init?.method ?? "GET"} ${path}`);
    }));

    const view = await mountSurface({ response });
    await settle();

    // Every product group is in the conversation; the sheet shows one group's cards at a time.
    expect(container?.querySelectorAll("[data-result-target]")).toHaveLength(8);
    expect(view.querySelectorAll(".vt-candidate-card")).toHaveLength(8);
    expect(hydrationTargetIds).toHaveLength(5);
    expect(activeHydrations).toBe(5);

    await act(async () => {
      for (const targetId of hydrationTargetIds.slice(0, 5)) {
        const pool = pools.find((candidatePool) => candidatePool.targetId === targetId)!;
        pendingHydrations.get(targetId)?.resolve(jsonResponse({ ...workspace, pools: [pool] }));
      }
    });
    await settle();

    expect(hydrationTargetIds).toHaveLength(8);
    expect(activeHydrations).toBe(3);
    expect(maximumActiveHydrations).toBe(5);
    expect(new Set(hydrationTargetIds)).toEqual(new Set(counts.map((_, index) => `target-${index + 1}`)));

    await act(async () => {
      for (const targetId of hydrationTargetIds.slice(5)) {
        const pool = pools.find((candidatePool) => candidatePool.targetId === targetId)!;
        pendingHydrations.get(targetId)?.resolve(jsonResponse({ ...workspace, pools: [pool] }));
      }
    });
    await settle();

    expect(activeHydrations).toBe(0);
    const cardsPerGroup: number[] = [];
    for (const tab of Array.from(document.querySelectorAll<HTMLButtonElement>('.curation-target-sheet [role="tab"]'))) {
      await act(async () => tab.click());
      cardsPerGroup.push(document.querySelectorAll(".curation-target-sheet .vt-candidate-card:not(.is-loading)").length);
    }
    // The first tab is "All": every group's cards at once.
    expect(cardsPerGroup).toEqual([52, ...counts]);
  });

  it("shows a terminal Research failure instead of an endless empty investigation", async () => {
    installCatalogFetch();
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0] = {
      ...response.research.groups[0],
      round: {
        id: "round-failed",
        shoppingSessionId: "session-1",
        userId: "user-1",
        roundNumber: 1,
        contextSchema: "vitlane.research-context.v1",
        contextVersion: 1,
        contextHash: "context-hash",
        status: "FAILED",
        failureReasonCode: "CANDIDATE_RANKING_EMPTY",
        failureRetryable: false,
        createdAt: "2026-08-13T00:00:00Z",
        completedAt: "2026-08-13T00:01:00Z",
      },
      rounds: [],
    };

    const view = await mountSurface({ response });
    await settle();

    expect(view.textContent).toContain("조사를 완료하지 못했습니다");
    expect(view.textContent).toContain(
      "조건과 확인 가능한 정보를 모두 만족하는 상품을 찾지 못했습니다.",
    );
    expect(view.textContent).not.toContain("CANDIDATE_RANKING_EMPTY");
    expect(view.textContent).not.toContain(
      "Vitlane이 이 상품의 추천 상품을 찾고 있습니다.",
    );
  });

  it("shows queued and running research on the owning target via the round mapping", async () => {
    installCatalogFetch();
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0].session.currentResearchRoundId = "round-async";
    response.intelligence = [
      {
        jobId: "job-async",
        actionId: "action-async",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-async",
        provider: "MANAGED",
        status: "PENDING",
        retryable: false,
        attempt: 1,
        steps: [],
      },
    ];

    const view = await mountSurface({ response });
    await settle();
    expect(view.textContent).toContain("조사를 기다리고 있습니다.");
    expect(view.textContent).not.toContain(
      "재조사 또는 상품 더 찾기로 Shopify 추천 상품을 불러오세요.",
    );

    const running = {
      ...response,
      intelligence: [{ ...response.intelligence![0], status: "RUNNING" as const }],
    };
    await act(async () => root?.render(surface({ response: running })));
    await settle();
    expect(view.textContent).toContain("Vitlane이 이 상품을 조사하고 있습니다.");
  });

  it("keeps the running strip visible while the target already shows candidates", async () => {
    const current = liveSearchPayload("existing-one", "Existing Field Pack");
    installCatalogFetch({ workspace: catalogWorkspacePayload(current.products) });
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0].session.currentResearchRoundId = "round-async";
    response.intelligence = [
      {
        jobId: "job-async",
        actionId: "action-async",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-async",
        provider: "MANAGED",
        status: "RUNNING",
        retryable: false,
        attempt: 1,
        steps: [],
      },
    ];

    const view = await mountSurface({ response });
    await settle();

    expect(view.textContent).toContain("Existing Field Pack");
    expect(view.textContent).toContain("Vitlane이 이 상품을 조사하고 있습니다.");
  });

  it("isolates a failed research job to its target and retries from there", async () => {
    const fetchMock = installCatalogFetch();
    const changed = vi.fn();
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0].session.currentResearchRoundId = "round-async";
    response.intelligence = [
      {
        jobId: "job-failed",
        actionId: "action-failed",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-async",
        provider: "MANAGED",
        status: "FAILED",
        failureCode: "PROVIDER_UNAVAILABLE",
        retryable: true,
        attempt: 1,
        steps: [],
      },
    ];

    const view = await mountSurface({ response, onWorkChanged: changed });
    await settle();

    const target = view.querySelector<HTMLElement>(".catalog-ui-target")!;
    expect(target.textContent).toContain("조사를 완료하지 못했습니다");
    expect(target.textContent).toContain("조사 지능을 사용할 수 없습니다.");
    await act(async () => button(target, "다시 시도").click());
    await settle();
    expect(callsTo(fetchMock, "/intelligence/jobs/job-failed/retry", "POST")).toBe(1);
    expect(changed).toHaveBeenCalled();
  });

  it("does not resurrect an old failed job after a newer success for the target", async () => {
    installCatalogFetch();
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0] = {
      ...response.research.groups[0],
      round: {
        id: "round-new",
        shoppingSessionId: "session-1",
        userId: "user-1",
        roundNumber: 2,
        contextSchema: "vitlane.research-context.v1",
        contextVersion: 1,
        contextHash: "context-hash",
        status: "RESULTS_READY",
        createdAt: "2026-08-13T00:02:00Z",
        completedAt: "2026-08-13T00:03:00Z",
      },
      rounds: [
        {
          id: "round-old",
          shoppingSessionId: "session-1",
          userId: "user-1",
          roundNumber: 1,
          contextSchema: "vitlane.research-context.v1",
          contextVersion: 1,
          contextHash: "context-hash",
          status: "FAILED",
          failureReasonCode: "PROVIDER_UNAVAILABLE",
          failureRetryable: true,
          createdAt: "2026-08-13T00:00:00Z",
        },
      ],
    };
    response.intelligence = [
      {
        jobId: "job-old",
        actionId: "action-old",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-old",
        provider: "MANAGED",
        status: "FAILED",
        failureCode: "PROVIDER_UNAVAILABLE",
        retryable: true,
        attempt: 1,
        steps: [],
      },
      {
        jobId: "job-new",
        actionId: "action-new",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-new",
        provider: "MANAGED",
        status: "SUCCEEDED",
        retryable: false,
        attempt: 1,
        steps: [],
      },
    ];

    const view = await mountSurface({ response });
    await settle();

    expect(view.textContent).not.toContain("조사를 완료하지 못했습니다");
    expect(view.textContent).not.toContain("다시 시도");
  });

  it("explains a terminal no-results outcome instead of looking stuck", async () => {
    installCatalogFetch();
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0] = {
      ...response.research.groups[0],
      round: {
        id: "round-empty",
        shoppingSessionId: "session-1",
        userId: "user-1",
        roundNumber: 1,
        contextSchema: "vitlane.research-context.v1",
        contextVersion: 1,
        contextHash: "context-hash",
        status: "NO_RESULTS",
        createdAt: "2026-08-13T00:00:00Z",
        completedAt: "2026-08-13T00:01:00Z",
      },
      rounds: [],
    };

    const view = await mountSurface({ response });
    await settle();

    expect(view.textContent).toContain(
      "조사 국가와 조건에 맞는 상품을 찾지 못했습니다.",
    );
    expect(view.textContent).not.toContain(
      "Vitlane이 이 상품의 추천 상품을 찾고 있습니다.",
    );
  });

  it("aggregates saved variant interactions into corner badges on the candidate card", async () => {
    const current = liveSearchPayload("signal-one", "Signal Field Pack");
    const workspace = catalogWorkspacePayload(current.products);
    workspace.interactions = [
      {
        candidateId: "signal-one",
        variantId: "variant-a",
        pinned: true,
        sentiment: "LIKE",
      },
      {
        candidateId: "signal-one",
        variantId: "variant-b",
        pinned: false,
        sentiment: "DISLIKE",
      },
    ];
    installCatalogFetch({ workspace });
    const view = await mountSurface();
    await settle();

    const card = Array.from(view.querySelectorAll(".vt-candidate-card")).find(
      (candidate) => candidate.textContent?.includes("Signal Field Pack"),
    )!;
    const actions = card.querySelectorAll('.vt-candidate-card__signal');
    expect(actions).toHaveLength(3);
    await act(async () => button(card, "Signal Field Pack 옵션 보기").click()); await settle();
    expect(document.body.querySelector('.catalog-ui-candidate-modal[role="dialog"]')).not.toBeNull();
  });

  it("places the Shopify source badge between the Candidate title and price without an empty disclosure section", async () => {
    const current = liveSearchPayload("shopify-source", "Source Identified Pack");
    installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
    });
    const view = await mountSurface();
    await settle();

    const card = Array.from(view.querySelectorAll(".vt-candidate-card")).find(
      (candidate) => candidate.textContent?.includes("Source Identified Pack"),
    )!;
    const badge = card.querySelector<HTMLElement>('.candidate-source-badge[data-source="SHOPIFY"]')!;
    const title = card.querySelector<HTMLElement>(".vt-candidate-card__title")!;
    const price = card.querySelector<HTMLElement>(".vt-candidate-card__price")!;

    expect(title.nextElementSibling?.contains(badge)).toBe(true);
    expect(badge.textContent).toBe("Shopify");
    expect(badge.querySelector("svg")).toBeNull();
    expect(card.compareDocumentPosition(badge) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(badge.compareDocumentPosition(price) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(card.closest(".catalog-ui-candidate-entry")?.querySelector(".catalog-ui-search-platform"))
      .toBeNull();

    await act(async () => button(card, "Source Identified Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!;
    expect(modal.querySelector(".catalog-ui-candidate-search-platform")).toBeNull();
    expect(modal.querySelectorAll(".candidate-detail__section")).toHaveLength(5);
    expect(modal.querySelectorAll('.vt-disclosure__trigger[aria-expanded="true"]')).toHaveLength(5);
  });

  it("renders only mandatory Shopify information line by line above the open assessment and research notes in the modal", async () => {
    const messages: LiveCatalogProviderMessage[] = [
      {
        type: "warning",
        code: "merchant_disclosure",
        path: "$.products[0]",
        contentType: "markdown",
        content: "Sold and shipped by **Example Shop**.",
        severity: "warning",
        presentation: "disclosure",
        imageUrl: "https://cdn.shopify.com/disclosure.png",
        url: "https://shop.example/policies/shipping",
      },
      {
        type: "warning",
        code: "market_notice",
        path: "$.products[0]",
        content: "Price and availability depend on the selected market.",
      },
      {
        type: "warning",
        code: "unsafe_notice",
        path: "$.products[0]",
        content: "This required notice remains visible without unsafe resources.",
        presentation: "notice",
        imageUrl: "javascript:alert(1)",
        url: "http://shop.example/unsafe",
      },
      {
        type: "info",
        path: "$.products[0]",
        content: "Optional provider information must stay hidden.",
        presentation: "notice",
      },
      {
        type: "info",
        path: "$.products[0]",
        content: "Disclosure remains mandatory regardless of message type.",
        presentation: "disclosure",
      },
      {
        type: "warning",
        path: "$.products[0]",
        content: "Unknown presentation falls back to a visible notice.",
        presentation: "toast",
      },
    ];
    installCatalogFetch({
      searches: [liveSearchPayload("live-disclosure", "Live Disclosure Pack", messages)],
    });
    const view = await mountSurface();

    await settle();

    const liveCard = Array.from(view.querySelectorAll(".vt-candidate-card")).find(
      (card) => card.textContent?.includes("Live Disclosure Pack"),
    )!;
    expect(liveCard.closest(".catalog-ui-candidate-entry")?.querySelector(".catalog-ui-search-platform"))
      .toBeNull();
    await act(async () => button(liveCard, "Live Disclosure Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!;
    const platform = modal.querySelector<HTMLElement>(".catalog-ui-candidate-search-platform")!;
    expect(platform.tagName).toBe("SECTION");
    expect(platform.getAttribute("aria-label")).toContain("Live Disclosure Pack");
    expect(platform.querySelectorAll('[role="note"]')).toHaveLength(5);
    expect(platform.querySelectorAll(":scope > ul > li")).toHaveLength(5);
    expect(platform.querySelectorAll("blockquote")).toHaveLength(0);
    expect(platform.nextElementSibling?.querySelector('.vt-disclosure__trigger[aria-expanded="true"]')?.textContent).toBe("조사 당시 평가");
    expect(platform.textContent).toContain("Sold and shipped by **Example Shop**.");
    expect(platform.textContent).toContain("This required notice remains visible");
    expect(platform.textContent).not.toContain("Optional provider information");
    expect(platform.textContent).toContain("Disclosure remains mandatory");
    expect(platform.textContent).toContain("Unknown presentation falls back");
    expect(platform.querySelectorAll("button")).toHaveLength(0);
    expect(platform.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    // The header shows the neutral merchant badge; the notice's image is the provider's.
    expect(platform.querySelector("header .curation-mall-logo")?.getAttribute("alt")).toBe("");
    const image = platform.querySelector("img:not(.curation-mall-logo)")!;
    expect(image.getAttribute("src")).toBe("https://cdn.shopify.com/disclosure.png");
    expect(image.getAttribute("alt")).toContain("Shopify 안내 이미지");
    const link = platform.querySelector("a")!;
    expect(link.getAttribute("href")).toBe("https://shop.example/policies/shipping");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toContain("noopener");
    expect(platform.querySelectorAll("img:not(.curation-mall-logo)")).toHaveLength(1);
    expect(platform.querySelectorAll("a")).toHaveLength(1);
  });

  it("restores response-scoped Shopify messages from the fresh workspace lookup", async () => {
    const live = liveSearchPayload("live-reload", "Reloaded Pack");
    installCatalogFetch({
      workspace: {
        schemaVersion: "vitlane.catalog-research-hydration.v1",
        messages: [{
          type: "warning",
          content: "Workspace market notice",
          presentation: "notice",
          subjectKind: "SEARCH",
        }],
        pools: [{
          targetId: "target-1",
          version: 2,
          expandOrdinal: 1,
          latestMode: "APPEND",
          latestDurationMilliseconds: 120,
          latestShopifyCalls: 1,
          latestRateRemaining: 9,
          products: live.products,
          hiddenProducts: [],
          messages: [{
            type: "warning",
            content: "Reloaded merchant disclosure",
            presentation: "disclosure",
            subjectKind: "PRODUCT",
            subjectRef: "live-reload",
          }],
        }],
        configurations: [],
        interactions: [],
        metrics: liveMetrics(),
      },
    });
    const view = await mountSurface();
    await settle();

    expect(view.textContent).toContain("Workspace market notice");
    const reloadedCard = Array.from(view.querySelectorAll(".vt-candidate-card")).find(
      (card) => card.textContent?.includes("Reloaded Pack"),
    )!;
    expect(reloadedCard.closest(".catalog-ui-candidate-entry")?.textContent)
      .not.toContain("Reloaded merchant disclosure");
    await act(async () => button(reloadedCard, "Reloaded Pack 옵션 보기").click());
    await settle();
    expect(document.body.querySelector(".catalog-ui-candidate-search-platform")?.textContent)
      .toContain("Reloaded merchant disclosure");
  });

  it("does not render legacy Candidate rows outside the Phase 8 pool authority", async () => {
    installCatalogFetch();
    const response = workspaceResponse();
    (response.research.groups[0] as unknown as { candidates: unknown[] }).candidates = [{
      id: "candidate-legacy",
      name: "Legacy Field Pack",
      productUrl: "https://example.test/legacy-pack",
    }];
    const view = await mountSurface({ response });
    await settle();

    expect(view.querySelectorAll(".vt-candidate-card")).toHaveLength(0);
    expect(view.textContent).not.toContain("Legacy Field Pack");
    expect(view.querySelectorAll('[aria-label="Legacy Field Pack 옵션 보기"]')).toHaveLength(0);
    expect(view.textContent).toContain("후보 0/50");
    expect(view.textContent).not.toContain("후보 더 찾기");
  });

  it("submits Research Again to the async worker and reloads the canonical pool on job completion", async () => {
    const current = liveSearchPayload("current-two", "Current Metro Pack");
    const replacement = liveSearchPayload("live-two", "Live Metro Pack");
    const replacementWorkspace = catalogWorkspacePayload(replacement.products);
    replacementWorkspace.pools[0].version = 2;
    const fetchMock = installCatalogFetch({
      workspaceSequence: [
        catalogWorkspacePayload(current.products),
        replacementWorkspace,
      ],
    });
    const onResearchAgain = vi.fn(async () => undefined);
    const view = await mountSurface({ onResearchAgain });
    await settle();

    await selectResearch(view);
    const textarea = view.querySelector<HTMLTextAreaElement>(
      '[aria-label="조사 요청"]',
    )!;
    await act(async () => {
      setTextareaValue(textarea, "waterproof commuter backpack");
    });
    await act(async () => button(view, "조사 요청 보내기").click());
    await settle();

    expect(onResearchAgain).toHaveBeenCalledWith(
      "target-1",
      "waterproof commuter backpack",
    );
    expect(callsTo(fetchMock, "/targets/target-1/catalog-research/expansions")).toBe(0);
    expect(view.textContent).toContain("Current Metro Pack");
    // The route-level transcript owns the optimistic user action and the
    // Server activity panel owns acknowledgement. The artifact must not add a
    // second transient "started" bubble for the same command.
    expect(view.textContent).not.toContain("재조사를 시작했습니다");

    const running = workspaceResponse();
    running.intelligence = [{
      jobId: "research-job-1", actionId: "action-1",
      targetKind: "RESEARCH_ROUND", targetId: "round-1",
      provider: "MANAGED", status: "RUNNING", retryable: false,
      attempt: 1, steps: [{
        id: "step-1", kind: "SEARCHING_CATALOG", status: "RUNNING",
        startedAt: "2026-08-13T00:01:00Z",
      }],
    }];
    await act(async () => root?.render(surface({
      response: running,
      onResearchAgain,
    })));
    await settle();
    expect(view.textContent).toContain("Current Metro Pack");
    expect(callsToFreshWorkspace(fetchMock)).toBe(1);
    expect(callsTo(fetchMock, "/catalog-research/hydrations")).toBe(1);
    expect(callsTo(fetchMock, "/cart")).toBe(1);

    const completed = workspaceResponse();
    completed.catalogResearch = storedWorkspacePayload(
      replacementWorkspace,
    ) as unknown as CurationWorkspaceResponse["catalogResearch"];
    completed.intelligence = [{
      ...running.intelligence[0],
      status: "SUCCEEDED",
      steps: [{
        id: "step-1", kind: "SEARCHING_CATALOG", status: "SUCCEEDED",
        startedAt: "2026-08-13T00:01:00Z",
        completedAt: "2026-08-13T00:01:03Z",
      }],
    }];
    await act(async () => root?.render(surface({
      response: completed,
      onResearchAgain,
    })));
    await settle();
    expect(view.textContent).toContain("Live Metro Pack");
    expect(view.textContent).not.toContain("Current Metro Pack");
    expect(callsTo(fetchMock, "/catalog-research/hydrations")).toBe(2);
  });

  it("renders the canonical REPLACE order returned by the workspace", async () => {
    const pinned = liveSearchPayload("pinned-one", "Pinned Legacy Pack").products[0];
    const fresh = liveSearchPayload("fresh-one", "Fresh Metro Pack").products[0];
    const workspace = catalogWorkspacePayload([fresh, pinned]);
    workspace.pools[0].latestMode = "REPLACE";
    workspace.interactions = [{
      candidateId: pinned.candidateId,
      variantId: pinned.previewVariant!.id,
      pinned: true,
      sentiment: "NONE",
    }];
    installCatalogFetch({ workspace });
    const view = await mountSurface();
    await settle();

    const titles = Array.from(view.querySelectorAll(".vt-candidate-card"))
      .map((card) => card.textContent ?? "");
    expect(titles).toHaveLength(2);
    expect(titles[0]).toContain("Fresh Metro Pack");
    expect(titles[1]).toContain("Pinned Legacy Pack");
    expect(view.querySelector(".phase8-target__provider-proof")).toBeNull();
    expect(view.textContent).not.toContain("SHOPIFY LIVE");
    expect(view.textContent).not.toContain("REPLACED");
  });

  it.each(["ko-KR", "en-US"])("opens an all-hidden pool after reload in %s", async locale => {
    window.localStorage.setItem("vitlane.locale.v1", locale);
    document.cookie = "vt_locale_choice=" + locale + "; Path=/";
    const hidden = liveSearchPayload("hidden-only", "Hidden Only Pack").products[0];
    const workspace = catalogWorkspacePayload([]);
    workspace.pools[0].hiddenProducts = [hidden];
    installCatalogFetch({ workspace });
    const view = await mountSurface({}, true);
    await act(async () => root?.render(<LocaleProvider>{surface()}</LocaleProvider>));
    await openSheet();
    const show = locale === "ko-KR" ? "숨긴 후보 1" : "Hidden 1";
    const hide = locale === "ko-KR" ? "기본 보기" : "Default view";
    expect(document.body.querySelectorAll(".vt-candidate-card")).toHaveLength(0);
    await act(async () => button(document.body, show).click());
    await settle();
    expect(document.body.textContent).toContain("Hidden Only Pack");
    await act(async () => button(document.body, hide).click());
    await settle();
    expect(document.body.querySelectorAll(".vt-candidate-card")).toHaveLength(0);
    expect(button(document.body, show)).toBeTruthy();
    expect(view.querySelector(".curation-result__list")?.getAttribute("aria-label")).toContain(locale === "ko-KR" ? "후보 1개 펼치기" : "Open all 1 candidates");
    expect(view.querySelector(".curation-result__list-label")).toBeNull();
  });

  it("hydrates hidden Candidates only on demand and once per pool version", async () => {
    const visible = liveSearchPayload("visible-lazy", "Visible Lazy Pack").products[0];
    const hidden = liveSearchPayload("hidden-lazy", "Hidden Lazy Pack").products[0];
    const workspace = catalogWorkspacePayload([visible]);
    workspace.pools[0].hiddenProducts = [hidden];
    const nextWorkspace = catalogWorkspacePayload([visible]);
    nextWorkspace.pools[0].version = 2;
    nextWorkspace.pools[0].hiddenProducts = [hidden];
    const fetchMock = installCatalogFetch({
      workspace,
      workspaceSequence: [workspace, workspace, nextWorkspace, nextWorkspace],
    });
    const view = await mountSurface();
    await settle();

    expect(callsToFreshWorkspace(fetchMock)).toBe(1);
    await act(async () => button(view, "숨긴 후보 1").click());
    await settle();
    expect(view.textContent).toContain("Hidden Lazy Pack");
    expect(callsToFreshWorkspace(fetchMock)).toBe(2);

    await act(async () => button(view, "기본 보기").click());
    await act(async () => button(view, "숨긴 후보 1").click());
    await settle();
    expect(callsToFreshWorkspace(fetchMock)).toBe(2);

    await act(async () => button(view, "기본 보기").click());
    const nextResponse = workspaceResponse();
    nextResponse.catalogResearch = storedWorkspacePayload(nextWorkspace) as unknown as CurationWorkspaceResponse["catalogResearch"];
    await act(async () => root?.render(surface({ response: nextResponse })));
    await settle();
    expect(callsToFreshWorkspace(fetchMock)).toBe(3);
    await act(async () => button(view, "숨긴 후보 1").click());
    await settle();
    expect(callsToFreshWorkspace(fetchMock)).toBe(4);
  });

  it("preserves the visible pool when an async Research Again completes empty", async () => {
    const current = liveSearchPayload("current-three", "Current Keep Pack");
    const initial = catalogWorkspacePayload(current.products);
    const completedEmpty = catalogWorkspacePayload(current.products);
    completedEmpty.pools[0].latestMode = "REPLACE";
    completedEmpty.pools[0].version = 2;
    installCatalogFetch({
      workspaceSequence: [initial, completedEmpty],
    });
    const onResearchAgain = vi.fn(async () => undefined);
    const view = await mountSurface({ onResearchAgain });
    await settle();

    await selectResearch(view);
    const textarea = view.querySelector<HTMLTextAreaElement>(
      '[aria-label="조사 요청"]',
    )!;
    await act(async () => setTextareaValue(textarea, "keep the current pool if empty"));
    await act(async () => button(view, "조사 요청 보내기").click());
    await settle();

    const completed = workspaceResponse();
    completed.catalogResearch = storedWorkspacePayload(
      completedEmpty,
    ) as unknown as CurationWorkspaceResponse["catalogResearch"];
    completed.intelligence = [{
      jobId: "research-job-empty", actionId: "action-empty",
      targetKind: "RESEARCH_ROUND", targetId: "round-empty",
      provider: "MANAGED", status: "SUCCEEDED", retryable: false,
      attempt: 1, steps: [],
    }];
    await act(async () => root?.render(surface({
      response: completed,
      onResearchAgain,
    })));
    await settle();
    expect(view.textContent).toContain("Current Keep Pack");
    expect(view.querySelector(".phase8-target__provider-proof")).toBeNull();
  });

  it("opens the Variant modal from the card and stores CartView items without a mutation call", async () => {
    const fetchMock = installCatalogFetch({
      searches: [liveSearchPayload("live-three", "Live Draft Pack")],
      preparation: preparationPayload(),
    });
    const view = await mountSurface();

    await settle();
    expect(view.textContent).toContain("Live Draft Pack");
    expect(callsTo(fetchMock, "/targets/target-1/catalog-research/expansions")).toBe(0);

    await act(async () => button(view, "Live Draft Pack 옵션 보기").click());
    await settle();
    expect(callsTo(fetchMock, "/variant-pages")).toBe(1);
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    expect(modal.querySelector('[role="radiogroup"]')?.getAttribute("aria-label")).toBe("옵션 선택");
    await act(async () => button(modal, "장바구니 담기").click());
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(1);
    expect(view.textContent).toContain("장바구니 제거");

    await act(async () => button(view, "Live Draft Pack 옵션 보기").click());
    await settle();
    const reopenedModal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    await act(async () => button(reopenedModal, "한 개 더 담기").click());
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(2);
    const cartWrites = fetchMock.mock.calls.filter(([input, init]) =>
      String(input).endsWith("/cart") && (init?.method ?? "GET") === "PUT"
    );
    const secondCartBody = JSON.parse(String(cartWrites[1][1]?.body));
    expect(secondCartBody.items[0].quantity).toBe(2);
    expect(secondCartBody.items[0]).not.toHaveProperty("addedAt");

    await act(async () => button(view, "장바구니 1").click());
    await act(async () => button(document.body, "주문하기").click());
    await settle();

    expect(callsTo(fetchMock, "/prepare-agency-order", "POST")).toBe(1);
    expect(document.body.textContent).toContain("예상 합계");
    expect(document.body.textContent).not.toContain("READY_FOR_AGENCY_ORDER");
    expect(document.body.textContent).not.toContain("Shopify calls");
  });

  it("shows Add to cart on an unconfigured card and opens the existing Variant modal", async () => {
    const current = liveSearchPayload("card-add", "Card Add Pack");
    const fetchMock = installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
    });
    const view = await mountSurface();
    await settle();

    expect(button(view, "장바구니 담기").disabled).toBe(false);
    await act(async () => button(view, "장바구니 담기").click());
    await settle();

    const modal = document.body.querySelector<HTMLElement>(
      ".catalog-ui-candidate-modal",
    );
    expect(modal).not.toBeNull();
    expect(modal?.textContent).toContain("Card Add Pack");
    expect(callsTo(fetchMock, "/variant-pages")).toBe(1);
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(0);
  });

  it("adds the saved available Variant from the card without opening the modal", async () => {
    const current = liveSearchPayload("saved-card-add", "Saved Card Add Pack");
    const savedVariant = {
      ...variantPagePayload().rows[1],
      variantId: "gid://shopify/ProductVariant/saved-card-add",
    };
    const workspace = catalogWorkspacePayload(current.products);
    workspace.configurations = [{
      candidateId: "saved-card-add",
      variant: savedVariant,
      observedAt: "2026-08-13T00:00:45Z",
    }];
    const fetchMock = installCatalogFetch({ workspace });
    const view = await mountSurface();
    await settle();

    await act(async () => button(view, "장바구니 담기").click());
    await settle();

    expect(document.body.querySelector(".catalog-ui-candidate-modal")).toBeNull();
    expect(callsTo(fetchMock, "/variant-pages")).toBe(0);
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(1);
    const cartWrite = fetchMock.mock.calls.find(([input, init]) =>
      String(input).endsWith("/cart") && init?.method === "PUT"
    );
    expect(JSON.parse(String(cartWrite?.[1]?.body))).toMatchObject({
      items: [expect.objectContaining({
        candidateId: "saved-card-add",
        variantId: "gid://shopify/ProductVariant/saved-card-add",
        variantTitle: savedVariant.title,
        observedAt: "2026-08-13T00:00:45Z",
      })],
    });
    expect(view.textContent).toContain("장바구니 제거");
  });

  it("opens the existing modal when a saved Variant is unavailable", async () => {
    const current = liveSearchPayload("saved-unavailable", "Saved Unavailable Pack");
    const savedVariant = {
      ...variantPagePayload().rows[0],
      variantId: "gid://shopify/ProductVariant/saved-unavailable",
      available: false,
    };
    const workspace = catalogWorkspacePayload(current.products);
    workspace.configurations = [{
      candidateId: "saved-unavailable",
      variant: savedVariant,
      observedAt: "2026-08-13T00:00:45Z",
    }];
    const fetchMock = installCatalogFetch({
      workspace,
      variantPages: [{
        ...variantPagePayload(),
        candidateId: "saved-unavailable",
        rows: [savedVariant],
      }],
    });
    const view = await mountSurface();
    await settle();

    await act(async () => button(view, "장바구니 담기").click());
    await settle();

    expect(document.body.querySelector(".catalog-ui-candidate-modal")).not.toBeNull();
    expect(callsTo(fetchMock, "/variant-pages")).toBe(1);
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(0);
  });

  it("disables an available=false Variant and never sends a Cart mutation", async () => {
    const current = liveSearchPayload("sold-out", "Sold Out Dinnerware Set");
    current.products[0]!.previewVariant!.available = false;
    const variants = variantPagePayload();
    variants.candidateId = "sold-out";
    variants.productTitle = "Sold Out Dinnerware Set";
    variants.rows = [{
      variantId: current.products[0]!.previewVariant!.id,
      title: "Sky",
      priceMinor: 8200,
      currency: "USD",
      available: false,
      selectedOptions: [{ name: "Color", value: "Sky" }],
      productUrl: "https://shop.example/products/sold-out?variant=1",
    }];
    const fetchMock = installCatalogFetch({
      searches: [current],
      variantPages: [variants],
    });
    const view = await mountSurface();

    await settle();
    await act(async () => button(view, "Sold Out Dinnerware Set 옵션 보기").click());
    await settle();

    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    const unavailableRow = modal.querySelector<HTMLButtonElement>(".catalog-ui-variant-row");
    expect(unavailableRow?.disabled).toBe(true);
    expect(modal.textContent).toContain("현재 구매 불가");
    expect(button(modal, "구매 불가 — 다른 옵션 선택").disabled).toBe(true);
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(0);
  });

  it("opens OrderSheet only after the deployment capability is ready", async () => {
    const fetchMock = installCatalogFetch({
      searches: [liveSearchPayload("order-ready", "Order Ready Pack")],
    });
    const onOpenOrderSheet = vi.fn();
    const view = await mountSurface({ onOpenOrderSheet });

    await settle();
    await act(async () => button(view, "Order Ready Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>(
      ".catalog-ui-candidate-modal",
    )!;
    await act(async () => button(modal, "장바구니 담기").click());
    await act(async () => button(view, "장바구니 1").click());
    await settle();

    await act(async () => button(document.body, "주문하기").click());

    expect(callsTo(fetchMock, "/agency-order-capability")).toBe(1);
    expect(onOpenOrderSheet).toHaveBeenCalledWith(
      "/curations/curation-1/order-sheet?cartVersion=1",
    );
    expect(callsTo(fetchMock, "/prepare-agency-order")).toBe(0);
  });

  it("keeps the Cart and explains recovery when AgencyOrder is unavailable", async () => {
    const fetchMock = installCatalogFetch({
      searches: [liveSearchPayload("order-disabled", "Order Disabled Pack")],
      capability: agencyOrderCapabilityPayload("UNAVAILABLE"),
    });
    const onOpenOrderSheet = vi.fn();
    const view = await mountSurface({ onOpenOrderSheet });

    await settle();
    await act(async () => button(view, "Order Disabled Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>(
      ".catalog-ui-candidate-modal",
    )!;
    await act(async () => button(modal, "장바구니 담기").click());
    await act(async () => button(view, "장바구니 1").click());
    await settle();

    expect(button(document.body, "주문 기능 준비 중").disabled).toBe(true);
    expect(document.body.textContent).toContain("장바구니는 유지되며");
    expect(onOpenOrderSheet).not.toHaveBeenCalled();
    expect(callsTo(fetchMock, "/agency-order-capability")).toBe(1);
  });

  it("allows two different Variants of one Candidate and removes them together from the card", async () => {
    const fetchMock = installCatalogFetch({
      searches: [liveSearchPayload("live-three", "Live Multi Pack")],
    });
    const view = await mountSurface();

    await settle();
    await act(async () => button(view, "Live Multi Pack 옵션 보기").click());
    await settle();
    let modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    await act(async () => button(modal, "장바구니 담기").click());
    expect(view.textContent).toContain("장바구니 1");

    await act(async () => button(view, "Live Multi Pack 옵션 보기").click());
    await settle();
    modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    const rows = modal.querySelectorAll<HTMLButtonElement>(".catalog-ui-variant-row");
    await act(async () => rows[1].click());
    await act(async () => button(modal, "장바구니 담기").click());
    expect(view.textContent).toContain("장바구니 2");
    expect(callsTo(fetchMock, "/variant-pages")).toBe(2);

    await act(async () => button(view, "장바구니 제거").click());
    expect(view.textContent).toContain("장바구니 0");
    expect(view.textContent).not.toContain("장바구니 제거");
  });

  it("keeps the Variant modal open and explains a Cart save failure", async () => {
    const current = liveSearchPayload("cart-failure", "Cart Failure Pack");
    installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
      cartResponses: [errorResponse({
        code: "INVALID_INPUT",
        reasonCode: "PHASE8_CART_ITEM_INVALID",
        retryable: false,
      })],
    });
    const view = await mountSurface();
    await settle();

    await act(async () => button(view, "Cart Failure Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    await act(async () => button(modal, "장바구니 담기").click());
    await settle();

    expect(document.body.querySelector('.catalog-ui-candidate-modal[role="dialog"]')).not.toBeNull();
    expect(modal.textContent).toContain(
      "상품 정보가 아직 완전히 준비되지 않아 담지 못했습니다",
    );
    expect(view.textContent).toContain("장바구니를 저장하지 못했습니다. 이전 내용은 그대로 유지됩니다");
  });

  it("fails closed when Cart hydration fails and resumes only after an explicit successful reload", async () => {
    const current = liveSearchPayload("cart-reload", "Cart Reload Pack");
    const fetchMock = installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
      cartReadResponses: [
        errorResponse({
          code: "UPSTREAM_FAILURE",
          reasonCode: "PHASE8_CART_LOAD_FAILED",
          retryable: false,
        }),
        jsonResponse(cartPayload(4, [])),
      ],
    });
    const view = await mountSurface();
    await settle();

    expect(view.textContent).toContain("장바구니 상태를 확인하지 못했습니다");
    expect(button(view, "장바구니 다시 불러오기").disabled).toBe(false);

    await act(async () => button(view, "Cart Reload Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    expect(modal.textContent).toContain("장바구니를 확인하지 못했습니다");
    expect(button(modal, "장바구니 다시 불러오기").disabled).toBe(false);
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(0);

    await act(async () => button(modal, "장바구니 다시 불러오기").click());
    await settle();
    expect(button(modal, "장바구니 담기").disabled).toBe(false);

    await act(async () => button(modal, "장바구니 담기").click());
    const cartWrites = fetchMock.mock.calls.filter(([input, init]) =>
      String(input).endsWith("/cart") && init?.method === "PUT"
    );
    expect(cartWrites).toHaveLength(1);
    expect(JSON.parse(String(cartWrites[0][1]?.body)).expectedVersion).toBe(4);
  });

  it("replaces the screen with the latest Cart after a version conflict and requires confirmation", async () => {
    const current = liveSearchPayload("cart-conflict", "Cart Conflict Pack");
    const latestItem = cartItemFor(current.products[0], 1);
    const fetchMock = installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
      cartReadResponses: [
        jsonResponse(cartPayload(1, [])),
        jsonResponse(cartPayload(2, [latestItem])),
      ],
      cartResponses: [
        errorResponse({
          code: "CONFLICT",
          reasonCode: "PHASE8_CART_VERSION_CONFLICT",
          retryable: true,
        }),
        jsonResponse({}),
      ],
    });
    const view = await mountSurface();
    await settle();

    await act(async () => button(view, "Cart Conflict Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    await act(async () => button(modal, "장바구니 담기").click());
    await settle();

    expect(modal.textContent).toContain(
      "장바구니가 다른 화면에서 변경되었습니다. 최신 내용을 확인하고 다시 시도해 주세요.",
    );
    expect(view.textContent).toContain("장바구니 1");
    let cartWrites = fetchMock.mock.calls.filter(([input, init]) =>
      String(input).endsWith("/cart") && init?.method === "PUT"
    );
    expect(JSON.parse(String(cartWrites[0][1]?.body)).expectedVersion).toBe(1);

    await act(async () => button(modal, "한 개 더 담기").click());
    cartWrites = fetchMock.mock.calls.filter(([input, init]) =>
      String(input).endsWith("/cart") && init?.method === "PUT"
    );
    expect(cartWrites).toHaveLength(2);
    expect(JSON.parse(String(cartWrites[1][1]?.body))).toMatchObject({
      expectedVersion: 2,
      items: [expect.objectContaining({ quantity: 2 })],
    });
  });

  it("blocks every mutation when conflict recovery cannot verify the displayed Cart", async () => {
    const current = liveSearchPayload("cart-stale", "Cart Stale Pack");
    const displayedItem = cartItemFor(current.products[0], 1);
    const fetchMock = installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
      cartReadResponses: [
        jsonResponse(cartPayload(3, [displayedItem])),
        errorResponse({
          code: "UPSTREAM_FAILURE",
          reasonCode: "PHASE8_CART_LOAD_FAILED",
          retryable: false,
        }),
        jsonResponse(cartPayload(4, [displayedItem])),
      ],
      cartResponses: [errorResponse({
        code: "CONFLICT",
        reasonCode: "PHASE8_CART_VERSION_CONFLICT",
        retryable: true,
      })],
    });
    const view = await mountSurface();
    await settle();

    await act(async () => button(view, "Cart Stale Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    await act(async () => button(modal, "한 개 더 담기").click());
    await settle();
    await act(async () => button(modal, "상품 상세 닫기").click());
    await act(async () => button(view, "장바구니 확인 필요").click());
    await settle();

    const drawer = document.body.querySelector<HTMLElement>(
      '[role="dialog"][aria-labelledby="phase8-cart-title"]',
    )!;
    expect(drawer.textContent).toContain("장바구니를 확인하지 못했습니다. 변경하거나 주문하기 전에 다시 불러와 주세요");
    expect(drawer.querySelector<HTMLSelectElement>("select")?.disabled).toBe(true);
    expect(button(drawer, "옵션 변경").disabled).toBe(true);
    expect(button(drawer, "제거").disabled).toBe(true);
    expect(button(drawer, "주문하기").disabled).toBe(true);
    expect(callsTo(fetchMock, "/cart", "PUT")).toBe(1);

    await act(async () => button(drawer, "장바구니 다시 불러오기").click());
    await settle();
    expect(button(drawer, "옵션 변경").disabled).toBe(false);
    expect(button(drawer, "제거").disabled).toBe(false);
  });

  it("keeps one activity/live-tail region immediately before the sticky composer", async () => {
    installCatalogFetch();
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0].session.currentResearchRoundId = "round-async";
    response.activeWork = {
      workTargetId: "round-async",
      label: "Candidate 조사",
      status: "RUNNING",
      detail: "조사 지능이 작업하고 있습니다.",
    };
    response.intelligence = [
      {
        jobId: "job-running",
        actionId: "action-running",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-async",
        provider: "MANAGED",
        status: "RUNNING",
        retryable: false,
        attempt: 1,
        steps: [],
      },
    ];
    const view = await mountSurface({ response });
    await settle();

    const conversationLog = view.querySelector('[data-testid="phase8-conversation-history"]')!;
    const slot = view.querySelector('[data-testid="phase8-latest-slot"]')!;
    const composer = view.querySelector(".catalog-ui-composer-dock")!;
    expect(slot.textContent).toContain("취소");
    // 처리 중에는 슬롯에 처리 카드 하나만 있고 중복 메시지를 만들지 않는다.
    expect(slot.querySelectorAll(".curation-conversation-bubble")).toHaveLength(0);
    expect(conversationLog.compareDocumentPosition(slot) & Node.DOCUMENT_POSITION_FOLLOWING)
      .toBeTruthy();
    expect(slot.compareDocumentPosition(composer) & Node.DOCUMENT_POSITION_FOLLOWING)
      .toBeTruthy();
  });

  it("does not create a duplicate tail message when a durable async result completes", async () => {
    installCatalogFetch();
    const runningResponse = workspaceResponse() as unknown as CurationWorkspaceResponse;
    runningResponse.research.groups[0].session.currentResearchRoundId = "round-async";
    runningResponse.intelligence = [
      {
        jobId: "job-async",
        actionId: "action-async",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-async",
        provider: "MANAGED",
        status: "RUNNING",
        retryable: false,
        attempt: 1,
        steps: [],
      },
    ];
    const view = await mountSurface({ response: runningResponse });
    await settle();

    const completed = workspaceResponse() as unknown as CurationWorkspaceResponse;
    completed.research.groups[0] = {
      ...completed.research.groups[0],
      round: {
        id: "round-async",
        shoppingSessionId: "session-1",
        userId: "user-1",
        roundNumber: 1,
        contextSchema: "vitlane.research-context.v1",
        contextVersion: 1,
        contextHash: "context-hash",
        status: "NO_RESULTS",
        createdAt: "2026-08-13T00:00:00Z",
        completedAt: "2026-08-13T00:01:00Z",
      },
      rounds: [],
    };
    completed.intelligence = [
      { ...runningResponse.intelligence![0], status: "SUCCEEDED" },
    ];
    await act(async () => root?.render(surface({ response: completed })));
    await settle();

    expect(view.querySelector('[data-testid="phase8-latest-slot"]')).toBeNull();
    const conversationLog = view.querySelector('[data-testid="phase8-conversation-history"]')!;
    expect(conversationLog.textContent).not.toContain("조사 완료");
    expect(view.textContent).not.toContain("행동을 완료하지 못했습니다");
  });

  it("does not replay finished job history into the conversation on mount", async () => {
    installCatalogFetch();
    const response = workspaceResponse() as unknown as CurationWorkspaceResponse;
    response.research.groups[0].session.currentResearchRoundId = "round-async";
    response.intelligence = [
      {
        jobId: "job-old-failed",
        actionId: "action-old",
        targetKind: "RESEARCH_ROUND",
        targetId: "round-async",
        provider: "MANAGED",
        status: "FAILED",
        failureCode: "PROVIDER_UNAVAILABLE",
        retryable: true,
        attempt: 1,
        steps: [],
      },
    ];
    const view = await mountSurface({ response });
    await settle();

    // 종결 이력이나 idle 인사말을 하단에 재생하지 않는다.
    expect(view.querySelector('[data-testid="phase8-latest-slot"]')).toBeNull();
    expect(view.textContent).not.toContain("상품을 선택해 다시 조사하거나");
    const conversationLog = view.querySelector('[data-testid="phase8-conversation-history"]')!;
    expect(conversationLog.textContent).not.toContain("조사를 완료하지 못했습니다");
    expect(conversationLog.querySelectorAll(".curation-conversation-bubble")).toHaveLength(0);
  });

  it("uses opaque Storefront cursors for Next and cached Prev pages", async () => {
    const current = liveSearchPayload("current-page", "Current Paginated Pack");
    const first = variantPagePayload();
    first.rows = [first.rows[0]];
    first.pagination.hasNext = true;
    first.pagination.nextCursor = "vpc_opaque_page_two";
    const second = variantPagePayload();
    second.rows = [second.rows[1]];
    second.pagination.hasNext = false;
    first.candidateId = "current-page";
    second.candidateId = "current-page";
    const fetchMock = installCatalogFetch({
      variantPages: [first, second],
      workspace: catalogWorkspacePayload(current.products),
    });
    const view = await mountSurface();

    await settle();
    await act(async () => button(view, "Current Paginated Pack 옵션 보기").click());
    await settle();
    let modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    expect(modal.textContent).toContain("Black / Small");
    await act(async () => button(modal, "다음").click());
    await settle();
    modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    expect(modal.textContent).toContain("Blue / Medium");

    const variantCalls = fetchMock.mock.calls.filter(([input]) =>
      String(input).includes("/variant-pages"),
    );
    expect(variantCalls).toHaveLength(2);
    expect(JSON.parse(String(variantCalls[0][1]?.body))).toEqual({});
    expect(JSON.parse(String(variantCalls[1][1]?.body))).toEqual({
      cursorToken: "vpc_opaque_page_two",
    });

    await act(async () => button(modal, "이전").click());
    await settle();
    expect(document.body.textContent).toContain("Black / Small");
    expect(callsTo(fetchMock, "/variant-pages")).toBe(2);
  });

  it("opens Candidate cards and exposes reactions only for the selected Variant", async () => {
    const fetchMock = installCatalogFetch({
      searches: [liveSearchPayload("live-four", "Live Variant Pack")],
    });
    const view = await mountSurface();

    await settle();
    const liveCard = Array.from(view.querySelectorAll(".vt-candidate-card")).find(
      (card) => card.textContent?.includes("Live Variant Pack"),
    )!;
    expect(liveCard.querySelector(".vt-candidate-card__hit-area")).not.toBeNull();
    expect(liveCard.querySelector(".vt-candidate-card__icon-actions")).toBeNull();
    expect(liveCard.querySelector(".vt-candidate-card__signals")).toBeNull();

    await act(async () => button(liveCard, "Live Variant Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    await act(async () => button(modal, "좋아요").click());
    await settle();
    expect(button(modal, "좋아요").getAttribute("aria-pressed")).toBe("true");
    expect(liveCard.querySelectorAll(".vt-candidate-card__signal.is-like")).toHaveLength(1);
    expect(fetchMock).toHaveBeenLastCalledWith(
      "/api/v1/curations/curation-1/catalog-research/candidates/live-four/variants/gid%3A%2F%2Fshopify%2FProductVariant%2F301/interaction",
      expect.objectContaining({ method: "PUT" }),
    );
    const interactionCalls = fetchMock.mock.calls.filter(([input, init]) =>
      String(input).endsWith("/interaction") && init?.method === "PUT"
    );
    expect(interactionCalls).toHaveLength(1);
    expect(JSON.parse(String(interactionCalls[0][1]?.body))).toEqual(
      expect.objectContaining({
        sentiment: "LIKE",
        likedSnapshot: expect.objectContaining({
          productTitle: "Live Variant Pack",
          variantTitle: "Black / Small",
          merchant: "Shop Example",
        }),
      }),
    );
    const rows = modal.querySelectorAll<HTMLButtonElement>(".catalog-ui-variant-row");
    await act(async () => rows[1].click());
    expect(button(modal, "좋아요").getAttribute("aria-pressed")).toBe("false");
  });

  it("serializes rapid Pin and Like updates without losing either interaction axis", async () => {
    const firstInteraction = deferred<Response>();
    const secondInteraction = deferred<Response>();
    const current = liveSearchPayload("live-race", "Live Interaction Race Pack");
    const fetchMock = installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
      interactionResponses: [firstInteraction.promise, secondInteraction.promise],
    });
    const view = await mountSurface();
    await settle();
    await act(async () => button(view, "Live Interaction Race Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;

    await act(async () => {
      button(modal, "Pin").click();
      button(modal, "좋아요").click();
      await Promise.resolve();
    });
    expect(callsTo(fetchMock, "/interaction", "PUT")).toBe(1);

    firstInteraction.resolve(noContentResponse());
    await settle();
    expect(callsTo(fetchMock, "/interaction", "PUT")).toBe(2);
    secondInteraction.resolve(noContentResponse());
    await settle();

    const interactionBodies = fetchMock.mock.calls
      .filter(([input, init]) =>
        String(input).endsWith("/interaction") && init?.method === "PUT",
      )
      .map(([, init]) => JSON.parse(String(init?.body)));
    expect(interactionBodies).toEqual([
      expect.objectContaining({ pinned: true, sentiment: "NONE" }),
      expect.objectContaining({ pinned: true, sentiment: "LIKE" }),
    ]);
    expect(button(modal, "Pin 해제").getAttribute("aria-pressed")).toBe("true");
    expect(button(modal, "좋아요").getAttribute("aria-pressed")).toBe("true");
  });

  it("rolls back only the failed optimistic interaction to the latest acknowledgement", async () => {
    const current = liveSearchPayload("live-rollback", "Live Interaction Rollback Pack");
    installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
      interactionResponses: [
        noContentResponse(),
        errorResponse({
          code: "INTERNAL_FAILURE",
          reasonCode: "INTERACTION_WRITE_FAILED",
          retryable: false,
        }),
      ],
    });
    const view = await mountSurface();
    await settle();
    await act(async () => button(view, "Live Interaction Rollback Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;

    await act(async () => button(modal, "Pin").click());
    await settle();
    await act(async () => button(modal, "좋아요").click());
    await settle();

    expect(button(modal, "Pin 해제").getAttribute("aria-pressed")).toBe("true");
    expect(button(modal, "좋아요").getAttribute("aria-pressed")).toBe("false");
    expect(modal.textContent).toContain("반응을 저장하지 못했습니다. 다시 시도해 주세요");
  });

  it("saves a Variant separately from CartView and reopens with that option selected", async () => {
    installCatalogFetch({
      searches: [liveSearchPayload("live-save", "Live Saved Pack")],
    });
    const view = await mountSurface();

    await settle();
    await act(async () => button(view, "Live Saved Pack 옵션 보기").click());
    await settle();
    let modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    const rows = modal.querySelectorAll<HTMLButtonElement>(".catalog-ui-variant-row");
    await act(async () => rows[1].click());
    await act(async () => button(modal, "옵션 저장").click());

    expect(view.textContent).toContain("Blue / Medium");
    expect(view.textContent).toContain("84$");
    expect(view.textContent).toContain("장바구니 담기");
    expect(view.textContent).not.toContain("장바구니 제거");

    await act(async () => button(view, "Live Saved Pack 옵션 보기").click());
    await settle();
    modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    expect(modal.querySelectorAll<HTMLButtonElement>(".catalog-ui-variant-row")[1]
      .getAttribute("aria-checked")).toBe("true");
    expect(button(modal, "옵션 저장됨").disabled).toBe(true);
  });

  it("keeps a saved page-two Variant selected while the first Variant page loads", async () => {
    const current = liveSearchPayload("saved-page-two", "Saved Page Two Pack");
    const variants = variantPagePayload();
    const savedVariant = variants.rows[1];
    variants.candidateId = "saved-page-two";
    variants.rows = [variants.rows[0]];
    const workspace = {
      ...catalogWorkspacePayload(current.products),
      configurations: [{
        candidateId: "saved-page-two",
        variant: savedVariant,
        observedAt: variants.observedAt,
      }],
    };
    installCatalogFetch({ workspace, variantPages: [variants] });
    const view = await mountSurface();
    await settle();

    await act(async () => button(view, "Saved Page Two Pack 옵션 보기").click());
    await settle();
    const modal = document.body.querySelector<HTMLElement>('.catalog-ui-candidate-modal[role="dialog"]')!;
    const identity = modal.querySelector<HTMLElement>(
      ".catalog-ui-candidate-modal__identity",
    )!;
    const firstPageRow = modal.querySelector<HTMLButtonElement>(
      ".catalog-ui-variant-row",
    )!;
    expect(identity.textContent).toContain("Blue / Medium");
    expect(identity.textContent).toContain("84$");
    expect(firstPageRow.getAttribute("aria-checked")).toBe("false");
    expect(button(modal, "옵션 저장됨").disabled).toBe(true);

    await act(async () => firstPageRow.click());
    expect(identity.textContent).toContain("Black / Small");
    expect(firstPageRow.getAttribute("aria-checked")).toBe("true");
  });

  it("keeps the CartView Variant selected when reopening onto a page that omits it", async () => {
    const current = liveSearchPayload("cart-page-two", "Cart Page Two Pack");
    const initialPage = variantPagePayload();
    initialPage.candidateId = "cart-page-two";
    const reopenedPage = variantPagePayload();
    reopenedPage.candidateId = "cart-page-two";
    reopenedPage.rows = [reopenedPage.rows[0]];
    installCatalogFetch({
      workspace: catalogWorkspacePayload(current.products),
      variantPages: [initialPage, reopenedPage],
    });
    const view = await mountSurface();
    await settle();

    await act(async () => button(view, "Cart Page Two Pack 옵션 보기").click());
    await settle();
    let modal = document.body.querySelector<HTMLElement>(
      ".catalog-ui-candidate-modal",
    )!;
    const rows = modal.querySelectorAll<HTMLButtonElement>(".catalog-ui-variant-row");
    await act(async () => rows[1].click());
    await act(async () => button(modal, "장바구니 담기").click());

    await act(async () => button(view, "장바구니 1").click());
    await act(async () => button(document.body, "옵션 변경").click());
    await settle();
    modal = document.body.querySelector<HTMLElement>(".catalog-ui-candidate-modal")!;
    const identity = modal.querySelector<HTMLElement>(
      ".catalog-ui-candidate-modal__identity",
    )!;
    const onlyFetchedRow = modal.querySelector<HTMLButtonElement>(
      ".catalog-ui-variant-row",
    )!;
    expect(identity.textContent).toContain("Blue / Medium");
    expect(identity.textContent).toContain("84$");
    expect(onlyFetchedRow.getAttribute("aria-checked")).toBe("false");
  });

  it("does not invent a browser-local Target when the server rejects Add Target", async () => {
    const failure = Object.assign(new Error("EXTERNAL_AGENT_RETIRED"), {
      code: "EXTERNAL_AGENT_RETIRED",
    });
    const onAddTargets = vi.fn().mockRejectedValue(failure);
    installCatalogFetch();
    const view = await mountSurface({ onAddTargets });

    await act(async () => view.querySelector<HTMLButtonElement>(".catalog-ui-focus-token")!.click());
    await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')].find(b=>b.textContent?.includes("상품 추가"))!.click());
    const textarea = view.querySelector<HTMLTextAreaElement>('[aria-label="조사 요청"]')!;
    await act(async () => setTextareaValue(textarea, "trail running hydration vest"));
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    await settle();

    expect(onAddTargets).toHaveBeenCalledWith("trail running hydration vest");
    expect(view.textContent).toContain("trail running hydration vest");
    expect(view.textContent).not.toContain("Added Trail Pack");
    expect(view.textContent).toContain("상품을 추가하지 못했습니다");
  });

  it("starts a newer Target projection without waiting for stale hydration", async () => {
    let workspaceRequests = 0;
    let cartRequests = 0;
    let releaseFirstHydration!: () => void;
    const initialWorkspace = catalogWorkspacePayload(
      liveSearchPayload("candidate-initial", "Initial Candidate").products,
    );
    surfaceCatalogResearchFixture = storedWorkspacePayload(initialWorkspace) as CatalogWorkspaceResponse;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/catalog-research/hydrations")) {
        workspaceRequests += 1;
        if (workspaceRequests === 1) {
          return new Promise<Response>((resolve) => {
            releaseFirstHydration = () => resolve(jsonResponse(initialWorkspace));
          });
        }
        return Promise.resolve(jsonResponse({
          schemaVersion: "vitlane.catalog-research-hydration.v1",
          messages: [],
          pools: [],
          configurations: [],
          interactions: [],
          metrics: liveMetrics(),
        }));
      }
      if (path.endsWith("/cart")) {
        cartRequests += 1;
        return Promise.resolve(jsonResponse(cartPayload(0, [])));
      }
      throw new Error(`unexpected fetch ${init?.method ?? "GET"} ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    await mountSurface();
    expect(workspaceRequests).toBe(1);

    const nextResponse = workspaceResponse();
    nextResponse.targets.push({
      ...nextResponse.targets[0],
      id: "target-2",
      title: "Added Target",
      orderIndex: 1,
    });
    const nextWorkspace = catalogWorkspacePayload(
      liveSearchPayload("candidate-next", "Next Candidate").products,
    );
    nextWorkspace.pools[0].version = 2;
    nextResponse.catalogResearch = storedWorkspacePayload(nextWorkspace) as unknown as CurationWorkspaceResponse["catalogResearch"];
    await act(async () => root?.render(surface({ response: nextResponse })));
    await settle();

    expect(workspaceRequests).toBe(2);
    await act(async () => releaseFirstHydration());
    await settle();
    expect(workspaceRequests).toBe(2);
    expect(cartRequests).toBe(1);
    expect(viewText(container)).not.toContain("PHASE8_WORKSPACE_UNAVAILABLE");
  });
});

function surface(overrides: Partial<ComponentProps<typeof CatalogCurationResearch>> = {}) {
  const response = overrides.response ?? workspaceResponse();
  if (!response.catalogResearch && surfaceCatalogResearchFixture) {
    response.catalogResearch = surfaceCatalogResearchFixture as unknown as CurationWorkspaceResponse["catalogResearch"];
  }
  return (
    <CatalogConversationHarness
      overrides={overrides}
      response={response}
    />
  );
}

function CatalogConversationHarness({
  overrides,
  response,
}: {
  overrides: Partial<ComponentProps<typeof CatalogCurationResearch>>;
  response: CurationWorkspaceResponse;
}) {
  const [messages, setMessages] = useState<ConversationMessage[]>([]);
  const sequence = useRef(0);
  const historicalMessages = messages.filter((message) => message.state !== "ACTIVE");
  const activeMessages = messages.filter((message) => message.state === "ACTIVE");

  function appendMessage(draft: ConversationDraft) {
    sequence.current += 1;
    setMessages((current) => {
      const preceding = reconcileConversationTurn(current, draft);
      return [
        ...preceding,
        {
          ...draft,
          id: `message-${sequence.current}`,
          createdAt: new Date(1_785_000_000_000 + sequence.current).toISOString(),
        },
      ];
    });
  }

  return (
    <>
      <div data-testid="phase8-conversation-history">
        {historicalMessages.map((message) => (
          <CurationConversationBubble
            key={message.id}
            message={message}
            locale="ko-KR"
          />
        ))}
      </div>
      <CatalogCurationResearch
        working={false}
        cartOpenRequest={0}
        onAddTargets={vi.fn()}
        onResearchAgain={vi.fn()}
        onCartCountChange={vi.fn()}
        onRemoveTarget={vi.fn()}
        {...overrides}
        response={response}
        conversationTail={activeMessages.length > 0 ? (
          <div className="curation-live-conversation-turn">
            {activeMessages.map((message) => (
              <CurationConversationBubble
                key={message.id}
                message={message}
                locale="ko-KR"
              />
            ))}
          </div>
        ) : undefined}
        onConversationMessage={appendMessage}
      />
    </>
  );
}

function button(root: ParentNode, name: string) {
  const found = Array.from(root.querySelectorAll("button")).find(
    (candidate) =>
      candidate.textContent?.trim() === name ||
      candidate.getAttribute("aria-label") === name,
  );
  if (!(found instanceof HTMLButtonElement)) {
    throw new Error(`button not found: ${name}`);
  }
  return found;
}

async function settle() {
  await act(async () => {
    await Promise.resolve();
    await new Promise((resolve) => window.setTimeout(resolve, 0));
  });
}

function setTextareaValue(element: HTMLTextAreaElement, value: string) {
  Object.getOwnPropertyDescriptor(
    HTMLTextAreaElement.prototype,
    "value",
  )?.set?.call(element, value);
  element.dispatchEvent(new Event("input", { bubbles: true }));
}

function workspaceResponse() {
  return {
    plan: {
      id: "plan-1",
      userId: "user-1",
      originalIntent: "a commuter backpack",
      planningMode: "SINGLE",
      executionMode: "EXPERIMENT",
      totalBudget: { amount: "100", currency: "USD" },
      locationContext: { country: "US" },
      researchScope: {
        country: "US",
        allowedItems: [],
        blockedItems: [],
        urlMode: "NONE",
      },
      createdAt: "2026-08-13T00:00:00Z",
    },
    curation: {
      id: "curation-1",
      shoppingPlanId: "plan-1",
      userId: "user-1",
      phase: "CURATING",
      version: 2,
      createdAt: "2026-08-13T00:00:00Z",
      updatedAt: "2026-08-13T00:00:00Z",
    },
    targets: [
      {
        id: "target-1",
        planId: "plan-1",
        curationId: "curation-1",
        userId: "user-1",
        title: "Commuter backpack",
        normalizedIntent: "lightweight commuter backpack",
        category: "Bags",
        allocatedBudget: { amount: "100", currency: "USD" },
        researchScope: {
          country: "US",
          allowedItems: [],
          blockedItems: [],
          urlMode: "NONE",
        },
        orderIndex: 0,
        targetHashSchema: "vitlane.plan-target.v1",
        version: 1,
        createdAt: "2026-08-13T00:00:00Z",
        updatedAt: "2026-08-13T00:00:00Z",
      },
    ],
    research: {
      groups: [
        {
          session: {
            id: "session-1",
            planTargetId: "target-1",
            status: "REVIEWING",
            version: 1,
          },
          candidates: [],
        },
      ],
    },
    cart: { selections: [] },
    availableActions: [],
    timeline: [],
    latestArtifact: "CURATION_BOARD",
  } as unknown as CurationWorkspaceResponse;
}

function liveSearchPayload(
  id: string,
  title: string,
  messages: LiveCatalogProviderMessage[] = [],
) {
  const product: LiveCatalogProduct = {
    candidateId: id,
    title,
    description: "Fresh Shopify catalog product",
    priceMinimumMinor: 8200,
    priceMaximumMinor: 8200,
    currency: "USD",
    categories: ["Bags"],
    features: [],
    specifications: [],
    locator: {
      kind: "PRODUCT_URL",
      productUrl: `https://shop.example/products/${id}`,
    },
    previewVariant: {
      id: `gid://shopify/ProductVariant/${id}`,
      title: "Default Title",
      priceMinor: 8200,
      currency: "USD",
      available: true,
      sellerName: "Shop Example",
      sellerDomain: "shop.example",
    },
  };
  return {
    schemaVersion: "vitlane.phase8-live-catalog-review.v1",
    source: "LIVE_SHOPIFY_GLOBAL_CATALOG",
    provider: "shopify",
    protocolVersion: "2026-04-08",
    outcome: "SUCCESS",
    products: [product],
    messages,
    candidateEligibleCount: 1,
    discardedNoLocatorCount: 0,
    hasNextPage: false,
    appliedFilterVerified: true,
    appliedFilterCapability: "dev.shopify.catalog.global",
    targetId: "target-1",
    poolVersion: 2,
    expandOrdinal: 1,
    mode: "APPEND",
    metrics: {
      policyVersion: "phase8-live-review.v1",
      durationMilliseconds: 245,
      aiCallCount: 0,
      aiCostUsd: "0.00",
      shopifyCallCount: 1,
      providerCostStatus: "NO_BILLING_CREDENTIAL",
      providerBillingCredential: false,
      localCallsUsed: 1,
      localCallsRemaining: 9,
      localRateLimit: 10,
      localRateWindowSeconds: 60,
      externalEffect: "CATALOG_READ_ONLY",
    },
  };
}

function catalogWorkspacePayload(
  products: ReturnType<typeof liveSearchPayload>["products"],
): CatalogWorkspaceResponse {
  return {
    schemaVersion: "vitlane.catalog-research-hydration.v1",
    messages: [],
    pools: [{
      targetId: "target-1",
      version: 1,
      expandOrdinal: 0,
      latestMode: "APPEND",
      latestDurationMilliseconds: 80,
      latestShopifyCalls: 1,
      latestRateRemaining: 9,
      products,
      hiddenProducts: [],
      messages: [],
    }],
    configurations: [],
    interactions: [],
    metrics: liveMetrics(),
  };
}

function preparationPayload() {
  return {
    schemaVersion: "vitlane.agency-order-preparation-preview.v1",
    source: "FRESH_SHOPIFY_LOOKUP",
    status: "READY_FOR_AGENCY_ORDER",
    lines: [
      {
        cartItemId: "cart:live-three:gid://shopify/ProductVariant/301",
        status: "READY",
        productTitle: "Live Draft Pack",
        variantId: "gid://shopify/ProductVariant/301",
        variantTitle: "Black / Small",
        currentPriceMinor: 8200,
        currency: "USD",
        previewPriceMinor: 8200,
        previewCurrency: "USD",
        quantity: 1,
        available: true,
        priceChanged: false,
      },
    ],
    subtotalMinor: 8200,
    currency: "USD",
    observedAt: "2026-08-13T00:01:00Z",
    provider: "shopify",
    protocolVersion: "2026-04-08",
    metrics: {
      policyVersion: "phase8-live-review.v1",
      durationMilliseconds: 180,
      aiCallCount: 0,
      aiCostUsd: "0.00",
      shopifyCallCount: 1,
      providerCostStatus: "NO_BILLING_CREDENTIAL",
      providerBillingCredential: false,
      localCallsUsed: 2,
      localCallsRemaining: 8,
      localRateLimit: 10,
      localRateWindowSeconds: 60,
      externalEffect: "CATALOG_READ_ONLY",
    },
  };
}

function variantPagePayload() {
  return {
    schemaVersion: "vitlane.phase8-live-variant-page.v2",
    source: "LIVE_SHOPIFY_STOREFRONT",
    candidateId: "live-three",
    productTitle: "Live Draft Pack",
    merchantDomain: "shop.example",
    rows: [
      {
        variantId: "gid://shopify/ProductVariant/301",
        title: "Black / Small",
        priceMinor: 8200,
        currency: "USD",
        available: true,
        selectedOptions: [
          { name: "Color", value: "Black" },
          { name: "Size", value: "Small" },
        ],
        productUrl: "https://shop.example/products/live-three?variant=301",
      },
      {
        variantId: "gid://shopify/ProductVariant/302",
        title: "Blue / Medium",
        priceMinor: 8400,
        currency: "USD",
        available: true,
        selectedOptions: [
          { name: "Color", value: "Blue" },
          { name: "Size", value: "Medium" },
        ],
        productUrl: "https://shop.example/products/live-three?variant=302",
      },
    ],
    pagination: {
      pageSize: 20,
      hasPrevious: false,
      hasNext: false,
      nextCursor: undefined as string | undefined,
    },
    observedAt: "2026-08-13T00:00:30Z",
    metrics: {
      policyVersion: "phase8-live-review.v1",
      durationMilliseconds: 120,
      aiCallCount: 0,
      aiCostUsd: "0.00",
      shopifyCallCount: 3,
      providerCostStatus: "NO_BILLING_CREDENTIAL",
      providerBillingCredential: false,
      localCallsUsed: 2,
      localCallsRemaining: 8,
      localRateLimit: 10,
      localRateWindowSeconds: 60,
      externalEffect: "CATALOG_READ_ONLY",
    },
  };
}

function installCatalogFetch({
  searches = [],
  preparation = preparationPayload(),
  variantPages = [variantPagePayload()],
  interactionResponses = [],
  cartReadResponses = [],
  cartResponses = [],
  workspaceSequence,
  capability = agencyOrderCapabilityPayload("READY"),
  workspace = {
    schemaVersion: "vitlane.catalog-research-hydration.v1",
    messages: [], pools: [], configurations: [], interactions: [],
    metrics: liveMetrics(),
  },
}: {
  searches?: unknown[];
  preparation?: unknown;
  variantPages?: unknown[];
  interactionResponses?: Array<Response | Promise<Response> | Error>;
  cartReadResponses?: Array<Response | Error>;
  cartResponses?: Array<Response | Error>;
  workspaceSequence?: unknown[];
  capability?: unknown;
  workspace?: unknown;
} = {}) {
  if ((workspace as CatalogWorkspaceResponse).pools?.length === 0 && searches[0] && typeof searches[0] === "object" && "products" in searches[0]) {
    const payload = searches[0] as ReturnType<typeof liveSearchPayload>;
    workspace = catalogWorkspacePayload(payload.products);
    (workspace as CatalogWorkspaceResponse).pools[0].messages = payload.messages;
  }
  let searchIndex = 0;
  let variantPageIndex = 0;
  let interactionIndex = 0;
  let cartReadResponseIndex = 0;
  let cartResponseIndex = 0;
  let workspaceIndex = 0;
  let pendingFreshWorkspace: unknown;
  let cartVersion = 0;
  let cartItems: unknown[] = [];
  surfaceCatalogResearchFixture = storedWorkspacePayload(
    workspaceSequence?.[0] ?? workspace,
  ) as CatalogWorkspaceResponse;
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const method = init?.method ?? "GET";
    if (path.endsWith("/criteria")) return jsonResponse(null);
    if (path.endsWith("/agency-order-capability")) {
      return jsonResponse(capability);
    }
    if (path.includes("/intelligence/jobs/") && path.endsWith("/retry")) {
      return jsonResponse({ jobId: "job-retried" });
    }
    if (path.includes("/targets/") && path.endsWith("/catalog-research/expansions")) {
      const payload = await searches[Math.min(searchIndex, searches.length - 1)];
      searchIndex += 1;
      if (isRejectedFixture(payload)) throw payload;
      if (payload && typeof payload === "object" && "replay" in payload && payload.replay === true) {
        workspaceIndex = Math.max(workspaceIndex, 1);
      }
      return jsonResponse(payload);
    }
    if (path.includes("/catalog-research/candidates/") && path.endsWith("/variant-pages")) {
      const payload = variantPages[Math.min(variantPageIndex, variantPages.length - 1)];
      variantPageIndex += 1;
      return jsonResponse(payload);
    }
    if (path.includes("/catalog-research/candidates/") && path.endsWith("/configuration")) {
      return noContentResponse();
    }
    if (path.includes("/catalog-research/candidates/") && path.endsWith("/interaction")) {
      if (interactionResponses.length === 0) return noContentResponse();
      const response = interactionResponses[
        Math.min(interactionIndex, interactionResponses.length - 1)
      ];
      interactionIndex += 1;
      if (response instanceof Error) throw response;
      return response;
    }
    if (path.includes("/catalog-research/hydrations")) {
      let payload: unknown;
      if (pendingFreshWorkspace !== undefined) {
        payload = pendingFreshWorkspace;
        pendingFreshWorkspace = undefined;
      } else {
        payload = workspaceSequence?.[
          Math.min(workspaceIndex, workspaceSequence.length - 1)
        ] ?? workspace;
        workspaceIndex += 1;
      }
      if (isRejectedFixture(payload)) throw payload;
      const request = JSON.parse(String(init?.body ?? "{}")) as {
        scope?: string;
        targetId?: string;
        candidateId?: string;
      };
      return jsonResponse(scopedHydrationPayload(
        payload,
        request.scope,
        request.targetId,
        request.candidateId,
      ));
    }
    if (path.endsWith("/cart") && method === "PUT") {
      if (cartResponses.length > 0) {
        const response = cartResponses[
          Math.min(cartResponseIndex, cartResponses.length - 1)
        ];
        cartResponseIndex += 1;
        if (response instanceof Error) throw response;
        if (!response.ok) return response;
      }
      const body = JSON.parse(String(init?.body)) as { items: unknown[] };
      cartVersion += 1;
      cartItems = body.items.map((item) => ({
        ...(item as object), addedAt: "2026-08-13T00:00:31Z",
      }));
      return jsonResponse(cartPayload(cartVersion, cartItems));
    }
    if (path.endsWith("/cart")) {
      if (cartReadResponses.length > 0) {
        const response = cartReadResponses[
          Math.min(cartReadResponseIndex, cartReadResponses.length - 1)
        ];
        cartReadResponseIndex += 1;
        if (response instanceof Error) throw response;
        return response;
      }
      return jsonResponse(cartPayload(cartVersion, cartItems));
    }
    if (path.endsWith("/prepare-agency-order")) {
      return jsonResponse(preparation);
    }
    throw new Error(`unexpected fetch ${method} ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function agencyOrderCapabilityPayload(state: "READY" | "UNAVAILABLE") {
  const rail = (
    paymentMethod: "TVITUSD" | "PAYPAL_SANDBOX" | "PAYPAL_LIVE",
    railState: "READY" | "UNAVAILABLE",
  ) => ({
    state: railState,
    orderIssueState: railState,
    paymentInitiationState: railState,
    paymentMethod,
    providerEnvironment: paymentMethod === "TVITUSD"
      ? "TESTNET"
      : paymentMethod === "PAYPAL_SANDBOX" ? "SANDBOX" : "LIVE",
    asset: paymentMethod === "TVITUSD" ? "TVITUSD" : "USD",
    economicEffect: paymentMethod === "PAYPAL_LIVE" ? "REAL_MONEY" : "NO_REAL_VALUE",
  });
  const railState = state === "READY" ? "READY" : "UNAVAILABLE";
  return {
    schemaVersion: "vitlane.agency-order-capability.v2",
    capability: {
      state,
      reasonCode: state === "READY" ? "" : "AGENCY_ORDER_DISABLED",
      checkoutProvider: state === "READY" ? "STUB" : "NONE",
      capabilityRevision: 1,
      paymentRails: {
        tvitusd: rail("TVITUSD", railState),
        paypalSandbox: rail("PAYPAL_SANDBOX", "UNAVAILABLE"),
        paypalLive: rail("PAYPAL_LIVE", "UNAVAILABLE"),
      },
    },
  };
}

function storedWorkspacePayload(payload: unknown) {
  if (!payload || typeof payload !== "object") return payload;
  const workspace = payload as CatalogWorkspaceResponse;
  return {
    ...workspace,
    messages: [],
    configurations: [],
    pools: (workspace.pools ?? []).map((pool) => ({
      ...pool,
      messages: [],
      products: pool.products.map(storedCandidateProduct),
      hiddenProducts: pool.hiddenProducts.map(storedCandidateProduct),
    })),
    metrics: liveMetrics(),
  };
}

function scopedHydrationPayload(
  payload: unknown,
  scope?: string,
  targetId?: string,
  candidateId?: string,
) {
  if (!payload || typeof payload !== "object") return payload;
  const workspace = payload as CatalogWorkspaceResponse;
  const pools = (workspace.pools ?? [])
    .filter((pool) => pool.targetId === targetId)
    .map((pool) => ({
      ...pool,
      products: scope === "CANDIDATE"
        ? pool.products.filter((product) => product.candidateId === candidateId)
        : scope === "HIDDEN_TARGET"
        ? pool.products.map(storedCandidateProduct)
        : pool.products,
      hiddenProducts: scope === "CANDIDATE"
        ? pool.hiddenProducts.filter((product) => product.candidateId === candidateId)
        : scope === "VISIBLE_TARGET"
        ? pool.hiddenProducts.map(storedCandidateProduct)
        : pool.hiddenProducts,
    }));
  const candidateIDs = new Set(pools.flatMap((pool) => [
    ...pool.products.map(({ candidateId }) => candidateId),
    ...pool.hiddenProducts.map(({ candidateId }) => candidateId),
  ]));
  return {
    ...workspace,
    pools,
    configurations: workspace.configurations.filter(
      ({ candidateId }) => candidateIDs.has(candidateId),
    ),
    interactions: workspace.interactions.filter(
      ({ candidateId }) => candidateIDs.has(candidateId),
    ),
  };
}

function workspaceHasCandidates(payload: unknown) {
  if (!payload || typeof payload !== "object") return false;
  return ((payload as CatalogWorkspaceResponse).pools ?? []).some(
    (pool) => pool.products.length + pool.hiddenProducts.length > 0,
  );
}

function storedCandidateProduct(product: LiveCatalogProduct): LiveCatalogProduct {
  return {
    ...product,
    title: "",
    description: "",
    priceMinimumMinor: 0,
    priceMaximumMinor: 0,
    currency: "",
    mediaUrl: undefined,
    mediaAlt: undefined,
    categories: [],
    previewVariant: undefined,
  };
}

function cartPayload(version: number, items: unknown[]) {
  return {
    schemaVersion: "vitlane.cart-view.v2",
    curationId: "curation-1",
    version,
    country: "US",
    currency: "USD",
    items,
  };
}

function cartItemFor(
  product: LiveCatalogProduct,
  quantity: number,
) {
  const variant = variantPagePayload().rows[0];
  return {
    cartItemId: `cart:${product.candidateId}:${variant.variantId}`,
    targetId: "target-1",
    candidateId: product.candidateId,
    productTitle: product.title,
    productUrl: variant.productUrl,
    merchantName: product.previewVariant?.sellerName,
    sellerDomain: product.previewVariant?.sellerDomain,
    intentPoint: product.intentPoint,
    variantId: variant.variantId,
    variantTitle: variant.title,
    selectedOptions: variant.selectedOptions.map(
      ({ name, value }) => `${name}: ${value}`,
    ),
    previewPriceMinor: variant.priceMinor,
    previewCurrency: variant.currency,
    quantity,
    observedAt: "2026-08-13T00:00:30Z",
    addedAt: "2026-08-13T00:00:31Z",
  };
}

function callsTo(fetchMock: ReturnType<typeof vi.fn>, fragment: string, method?: string) {
  return fetchMock.mock.calls.filter(([input, init]) =>
    String(input).includes(fragment) && (!method || (init?.method ?? "GET") === method)
  ).length;
}

function callsToFreshWorkspace(fetchMock: ReturnType<typeof vi.fn>) {
  return fetchMock.mock.calls.filter(([input]) =>
    String(input).includes("/catalog-research/hydrations"),
  ).length;
}

function liveMetrics() {
  return {
    policyVersion: "phase8-live-review.v1",
    durationMilliseconds: 0,
    aiCallCount: 0,
    aiCostUsd: "0.00",
    shopifyCallCount: 0,
    providerCostStatus: "NOT_CALLED",
    providerBillingCredential: false,
    localCallsUsed: 0,
    localCallsRemaining: 10,
    localRateLimit: 10,
    localRateWindowSeconds: 60,
    externalEffect: "CATALOG_READ_ONLY",
  };
}

function jsonResponse(payload: unknown) {
  return {
    ok: true,
    status: 200,
    json: async () => payload,
  } as Response;
}

function errorResponse(error: {
  code: string;
  reasonCode: string;
  retryable: boolean;
}) {
  return {
    ok: false,
    status: 500,
    json: async () => ({ error }),
  } as Response;
}

function noContentResponse() {
  return { ok: true, status: 204 } as Response;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function isRejectedFixture(value: unknown): value is Error | DOMException {
  return value instanceof Error || Boolean(
    value &&
      typeof value === "object" &&
      "name" in value &&
      value.name === "AbortError",
  );
}

function pendingUntilAbort(signal?: AbortSignal | null): Promise<Response> {
  return new Promise((_resolve, reject) => {
    signal?.addEventListener("abort", () => {
      reject(new DOMException("The operation was aborted", "AbortError"));
    }, { once: true });
  });
}

function viewText(node: ParentNode | undefined): string {
  return node?.textContent ?? "";
}

function modeOption(title:string):HTMLButtonElement{
 const found=[...document.querySelectorAll<HTMLButtonElement>('[role="option"]')].find(b=>b.querySelector("strong")?.textContent===title);
 if(!found)throw Error(`missing mode option ${title}`);return found;
}
async function selectResearch(view:ParentNode,title="Commuter backpack"){
 await act(async()=>button(view,"조사 방식: Auto. 다른 방식 선택").click());
 await act(async()=>modeOption("재조사").click());
 await act(async()=>modeOption(`재조사 · ${title}`).click());
}
