// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { CurationWorkspaceResponse } from "../domain/types";
import { WorkspaceArtifact } from "./CurationWorkspacePage";

describe("Planning common composer recovery", () => {
  let root: Root | undefined;
  let container: HTMLDivElement;
  afterEach(async () => {
    await act(async () => root?.unmount());
    document.body.innerHTML = ""; vi.unstubAllGlobals(); vi.clearAllMocks();
  });
  async function mount(status = "CANCELLED", working = false, refresh: () => Promise<void> = vi.fn(async () => {})) {
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    const noop = vi.fn(async () => {});
    const response = { curation: { id: `planning-${status}`, phase: "PLANNING", version: 7 },
      plan: { locationContext: { country: "US" } }, targets: [],
      intelligence: [{ jobId: "initial", actionId: "action", targetKind: "PLANNING_TASK", status, steps: [], attempt: 1, retryable: false }],
    } as unknown as CurationWorkspaceResponse;
    await act(async () => root!.render(<WorkspaceArtifact artifact={{ id: "a", kind: "TARGET_LIST", title: "Planning", summary: "", updatedAt: "2026-09-11T00:00:00Z" }}
      response={response} working={working} catalogCartOpenRequest={0} onCatalogCartCountChange={noop}
      onAddTargets={noop} onResearchAgain={noop} onRemoveTarget={noop} onStartCurating={noop} onWorkChanged={refresh} />));
    return { noop, refresh };
  }
  async function fill(value: string) {
    const input = container.querySelector("textarea")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
  async function send() {
    await act(async () => container.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
  }
  it("restarts after cancellation from the same Auto composer and blocks duplicates until canonical refresh", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({ status: "EXECUTED", decision: "ADD_TARGET" }), { status: 202 }));
    vi.stubGlobal("fetch", fetchMock);
    let release!: () => void;
    const refresh = vi.fn(() => new Promise<void>(resolve => { release = resolve; }));
    await mount("CANCELLED", false, refresh);
    expect(container.querySelectorAll(".catalog-ui-focus-composer")).toHaveLength(1);
    expect(container.querySelector(".curation-artifact-empty--recovery")).toBeNull();
    expect(container.querySelector(".curation-working-bar")).toBeNull();
    expect(container.textContent).toContain("조사를 취소했습니다.");
    expect(container.textContent).not.toContain("조사 항목 직접 추가");
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(false);
    await fill("다시 헤드셋을 조사해 줘"); await send(); await send();
    const commands = fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
    expect(commands).toHaveLength(1);
    expect(String(commands[0][0])).toContain("/planning-CANCELLED/conversation-requests");
    const body = JSON.parse(commands[0][1]!.body as string);
    expect(body).toMatchObject({ request: "다시 헤드셋을 조사해 줘", expectedCurationVersion: 7 });
    expect(body.clientRequestId).toBeTruthy();
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(true);
    await act(async () => release());
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(false);
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("");
  });
  it("does not treat an in-flight cancellation as terminal", async () => {
    const fetchMock = vi.fn(); vi.stubGlobal("fetch", fetchMock);
    await mount("RUNNING", true);
    expect(container.querySelector(".curation-working-bar")).toBeNull();
    expect(container.querySelector(".curation-step-spinner")).not.toBeNull();
    expect(container.querySelector(".curation-artifact-empty")).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(true);
    await send(); expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(0);
  });
  it("preserves the draft after a failed request and keeps explicit Add product available", async () => {
    const fetchMock = vi.fn(async () => { throw new Error("offline"); }); vi.stubGlobal("fetch", fetchMock);
    const { noop } = await mount("FAILED");
    await fill("재요청할 상품"); await send();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("재요청할 상품");
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(false);
    await act(async () => container.querySelector<HTMLButtonElement>('.catalog-ui-focus-token')!.click());
    await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="option"]')].find(b=>b.textContent?.includes("상품 추가"))!.click());
    await send(); expect(noop).toHaveBeenCalledWith("재요청할 상품");
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("");
  });
  // Planned product groups are result rows before their research (ADR-0089). No request holds these, so
  // they stay under the plan sentence at the end; a group with no budget of its own is "no limit", not 0.
  it("lists planned product groups as rows with their budget, and an unset budget reads as no limit", async () => {
    vi.stubGlobal("fetch", vi.fn());
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    const noop = vi.fn(async () => {}); const remove = vi.fn(async () => {});
    const response = { curation: { id: "planning-rows", phase: "PLANNING", version: 3 },
      plan: { locationContext: { country: "KR" } },
      targets: [
        { id: "pen", title: "만년필", normalizedIntent: "필기감 좋은 만년필", allocatedBudget: { amount: "30000", currency: "KRW" } },
        { id: "ink", title: "잉크", normalizedIntent: "잉크", allocatedBudget: { amount: "0", currency: "KRW" } },
      ],
      intelligence: [],
    } as unknown as CurationWorkspaceResponse;
    await act(async () => root!.render(<WorkspaceArtifact artifact={{ id: "a", kind: "TARGET_LIST", title: "Planning", summary: "", updatedAt: "2026-09-23T00:00:00Z" }}
      response={response} working={false} catalogCartOpenRequest={0} onCatalogCartCountChange={noop}
      onAddTargets={noop} onResearchAgain={noop} onRemoveTarget={remove} onStartCurating={noop} onWorkChanged={noop} />));
    const rows = [...container.querySelectorAll("[data-planning-target]")];
    expect(rows.map(row => row.getAttribute("data-planning-target"))).toEqual(["pen", "ink"]);
    expect(container.querySelector(".curation-planning-response .curation-response__text")!.textContent).toBe("2가지로 나눠 찾아볼게요.");
    expect(rows[0].querySelector(".curation-result__plan-note")!.textContent).toBe("예산 ₩30,000 · 필기감 좋은 만년필");
    expect(rows[1].querySelector(".curation-result__plan-note")!.textContent).toBe("예산 제한 없음");
    expect(rows[1].textContent).not.toContain("₩0");
    const out = rows[1].querySelector<HTMLButtonElement>(".curation-response__plan-remove")!;
    expect(out.getAttribute("aria-label")).toBe("잉크 해당 상품군 제거");
    await act(async () => out.click());
    expect(remove).toHaveBeenCalledWith("ink");
  });
});
