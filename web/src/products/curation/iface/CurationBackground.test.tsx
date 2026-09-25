// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BackgroundDiscoveryResponse, CurationBackground } from "./CurationBackground";
import { CurationWorkspace } from "./CurationWorkspace";
import { discoveryResponses } from "../app/useBackgroundResearch";
import { backgroundAction, readBackground, type BackgroundView, type ResearchFinding } from "../infra/backgroundApi";
import type { CurationWorkspaceModel } from "../domain/types";

vi.mock("../infra/backgroundApi", () => ({ backgroundAction: vi.fn(), readBackground: vi.fn(), backgroundChanged: "background-test" }));
vi.mock("../app/useThreads", () => ({ useCurationThreads: () => undefined }));
let locale = "ko-KR";
const l = (en: string, ko: string, values?: Record<string, string | number>) => Object.entries(values ?? {}).reduce((s, [k, v]) => s.replaceAll(`{${k}}`, String(v)), locale === "ko-KR" ? ko : en);
vi.mock("../../../shared/i18n", () => ({ useLocale: () => ({ locale, l }), localizeFixedCopy: (_en: string, ko: string) => ko, invariantContent: (v: string) => v }));
const finding: ResearchFinding = { id: "f1", subscriptionId: "sub", targetId: "kitchen", status: "NEW", reason: "Everyday cookware", createdAt: "2026-09-22T13:00:00Z", product: { schemaVersion: "vitlane.deal-product.v1", provider: "feed", externalId: "1", identity: "product:1", productRef: { source: "COUPANG", marketplace: "KR", productId: "123" }, title: "스테인리스 주방용품 세트", url: "https://example.test/product", country: "KR", currency: "KRW", priceMinor: 15000, observedAt: "2026-09-22T13:00:00Z", expiresAt: "2099-09-29T13:00:00Z" } };
const view: BackgroundView = { schemaVersion: "vitlane.background-research.v1", findings: [finding], subscriptions: [{ id: "sub", curationId: "c", targetId: "kitchen", status: "ACTIVE", createdAt: "2026-09-22T12:00:00Z", terms: { country: "KR", currency: "KRW", keywords: [], maximumMinor: 30000, expiresAt: "2099-09-29T13:00:00Z", criteria: { subject: { label: "주방용품", productType: "cookware" }, axes: [], exclusions: [] } } }] };
let root: Root; let host: HTMLDivElement;
beforeEach(() => {
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("CSS", { escape: (v: string) => v });
  vi.mocked(readBackground).mockResolvedValue(view);
});
afterEach(async () => { await act(async () => root.unmount()); document.body.innerHTML = ""; vi.clearAllMocks(); vi.unstubAllGlobals(); locale = "ko-KR"; });
function Demo() {
  const [selected, select] = useState<ResearchFinding | null>(null);
  return <CurationBackground curationId="c" view={view} refresh={async () => {}} finding={selected} onFindingChange={select} />;
}
const click = async (text: string) => { const button = Array.from(document.querySelectorAll("button")).find(b => b.textContent === text); expect(button).toBeDefined(); await act(async () => button!.click()); };

it.each(["ko-KR", "en-US"])("keeps only two entries inline and opens a portal list/detail in one sheet (%s)", async code => {
  locale = code; await act(async () => root.render(<Demo />));
  expect(host.querySelectorAll("button")).toHaveLength(2);
  expect(host.querySelector(".curation-background__product")).toBeNull();
  await click(code === "ko-KR" ? "발견 상품 보기" : "View discoveries");
  const row = document.querySelector<HTMLButtonElement>(".curation-background__product")!;
  expect(row).not.toBeNull(); expect(host.contains(row)).toBe(false);
  await act(async () => row.click());
  expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
  expect(document.querySelector(".curation-background__detail")?.textContent).toContain("15,000");
  expect(document.activeElement?.hasAttribute("data-background-back")).toBe(true);
  const actions = document.querySelector(".curation-background__actions")!;
  const primary = actions.querySelector<HTMLAnchorElement>("a.vt-button--primary")!;
  expect(primary.textContent).toBe(code === "ko-KR" ? "링크 가기" : "Open link");
  expect(primary.href).toBe(finding.product.url); expect(primary.target).toBe("_blank");
  const secondary = actions.querySelector("button.vt-button--secondary")!;
  expect(secondary.textContent).toBe(code === "ko-KR" ? "후보 추가" : "Add to candidates");
  expect(actions.firstElementChild).toBe(primary);

  await click(code === "ko-KR" ? "발견 상품 목록" : "All discoveries");
  expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
  expect(document.activeElement?.getAttribute("data-finding-id")).toBe("f1");
  vi.spyOn(document.querySelector<HTMLElement>('[role="dialog"]')!, "getClientRects").mockReturnValue([new DOMRect(0, 0, 100, 100)] as unknown as DOMRectList);
  await act(async () => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(backgroundAction).not.toHaveBeenCalled();
});

it("keeps discoveries at their delivery time after adding and separates later arrivals", () => {
  const first = discoveryResponses({ ...view, findings: [{ ...finding, status: "ADDED" }, { ...finding, id: "same-batch", createdAt: "2026-09-22T22:00:30+09:00" }, { ...finding, id: "later", createdAt: "2026-09-22T13:05:00Z" }, { ...finding, id: "hidden", status: "HIDDEN" }] });
  expect(first).toHaveLength(2); expect(first[0].findings.map(f => f.id)).toEqual(["f1", "same-batch"]);
  expect(first[0].createdAt).toBe(finding.createdAt);
  expect(first[0].id).toBe(discoveryResponses(view)[0].id);
});

it("places a discovery between earlier and later conversation turns, with controls at the dock", async () => {
  const workspace = { curation: { id: "c", phase: "CURATING" }, timeline: [{ id: "artifact", kind: "RESULT", createdAt: "2026-09-22T12:00:00Z", summary: "", artifact: { id: "artifact", title: "Existing results", updatedAt: "2026-09-22T12:00:00Z" } }], conversation: { messages: [], requests: [{ id: "before", body: "Before discovery", createdAt: "2026-09-22T12:00:00Z" }, { id: "after", body: "After discovery", createdAt: "2026-09-22T14:00:00Z" }] } } as unknown as CurationWorkspaceModel;
  await act(async () => root.render(<CurationWorkspace workspace={workspace} renderArtifact={() => <div data-existing-results />}/>));
  const text = host.querySelector(".curation-transcript")!.textContent!;
  expect(text.indexOf("Before discovery")).toBeLessThan(text.indexOf("지켜보던"));
  expect(text.indexOf("지켜보던")).toBeLessThan(text.indexOf("After discovery"));
  expect(host.querySelector("[data-existing-results]")).not.toBeNull();
  expect(host.querySelector(".curation-workspace__dock-shell .curation-background__controls")).not.toBeNull();
  expect(host.querySelector(".curation-workspace__main .curation-background__controls")).toBeNull();
});

it("shows every discovery as a compact card, keeps its price, and falls back when the image fails", async () => {
  const onOpen = vi.fn();
  const withImage = { ...finding, product: { ...finding.product, imageUrl: "https://cdn4.telesco.pe/file/product.jpg" } };
  const withoutImage = { ...finding, id: "f2" };
  await act(async () => root.render(<BackgroundDiscoveryResponse response={{ id: "batch", createdAt: finding.createdAt, subject: "주방용품", findings: [withImage, withoutImage] }} onOpen={onOpen} />));
  const cards = host.querySelectorAll<HTMLButtonElement>(".curation-discovery-card");
  expect(cards).toHaveLength(2); expect(cards[0].textContent).toContain("15,000");
  const image = cards[0].querySelector("img")!; expect(image.src).toBe(withImage.product.imageUrl);
  expect(cards[1].textContent).toContain("이미지 없음");
  await act(async () => image.dispatchEvent(new Event("error")));
  expect(cards[0].querySelector("img")).toBeNull(); expect(cards[0].textContent).toContain("이미지 없음");
  await act(async () => cards[1].click()); expect(onOpen).toHaveBeenCalledWith(withoutImage);
});
