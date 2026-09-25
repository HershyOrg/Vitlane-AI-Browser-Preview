// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { AmazonCandidate } from "./AmazonCandidate";
import { saveCatalogInteraction, type LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
vi.mock("../research/infra/amazonApi", async (original) => ({
  ...await original<typeof import("../research/infra/amazonApi")>(),
  queueAmazonRead: <T,>(operation: () => Promise<T>) => operation(),
}));
const ref = { source: "AMAZON", marketplace: "US", asin: "B012345678" } as const;
const product: LiveCatalogProduct = { candidateId: "amazon-card", source: "AMAZON", title: "Headset", description: "", priceMinimumMinor: 0, priceMaximumMinor: 0, currency: "USD", categories: [], features: [], specifications: [], variantObservation: { observationId: "test-observation", variantRef: ref, price: { kind: "UNKNOWN", reasonCode: "PRICE_NOT_OBSERVED" }, availability: "UNKNOWN", deliveryEligibility: "UNCONFIRMED", seller: { kind: "UNKNOWN" }, purchaseRoute: "EXTERNAL", productUrl: "https://www.amazon.com/dp/B012345678", observedAt: "2026-09-10T00:00:00Z", refreshAfter: "2026-09-10T00:15:00Z" } };
afterEach(() => { vi.unstubAllGlobals(); window.localStorage.clear(); });
for (const locale of ["en-US", "ko-KR"] as const) it(`${locale}: external purchase and offline purchase check never call Cart or product provider`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`; window.localStorage.setItem("vitlane.locale.v1", locale);
  let checked = false; let version = 0; const writes: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string, options?: RequestInit) => {
    if (url.endsWith("purchase-check")) {
      writes.push(url); const body = JSON.parse(options?.body as string); expect(body.expectedVersion).toBe(version); expect(body.variantRef).toEqual(ref); checked = body.checked; version++;
      // The check carries what the user saw (ADR-0075); an undo carries no snapshot.
      if (body.checked) expect(body.snapshot).toMatchObject({ productTitle: "Headset", merchant: "Amazon", priceUnknown: true, priceMinor: 0 }); else expect(body.snapshot).toBeUndefined();
      expect((options?.headers as Record<string, string>)["Idempotency-Key"]).toMatch(/^[0-9a-f-]{36}$/);
      return new Response(JSON.stringify({ version, records: [{ variantRef: ref, candidateId: "amazon-card", checked, version, evidence: "SELF_REPORTED" }] }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    else if (!url.endsWith("state")) throw new Error(`Unexpected provider or Cart call: ${url}`);
    return new Response(JSON.stringify({ variantRef: ref, configurationVersion: 0, purchaseFeedback: { version, records: version ? [{ variantRef: ref, candidateId: "amazon-card", checked, version, evidence: "SELF_REPORTED" }] : [] } }), { status: 200, headers: { "Content-Type": "application/json" } });
  }));
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  HTMLDialogElement.prototype.close = vi.fn(); HTMLDialogElement.prototype.showModal = vi.fn();
  const mount = async () => { await act(async () => root.render(<LocaleProvider><AmazonCandidate product={product} curationId="curation" targetId="target" targetTitle="Headsets" interactions={{}} onInteraction={vi.fn()} /></LocaleProvider>)); };
  await mount();
  const button = (label: string) => Array.from(el.querySelectorAll("button")).find((b) => b.textContent === label)!;
  const bodyButton = (label: string) => Array.from(document.body.querySelectorAll("button")).find((b) => b.textContent?.trim() === label)!;
  expect(el.textContent).not.toMatch(/Add to cart|장바구니 담기|OPENWEBNINJA|\$0\.00/);
  expect(button(locale === "ko-KR" ? "외부 구매" : "External purchase").disabled).toBe(false);
  await act(async () => button(locale === "ko-KR" ? "구매 체크" : "Purchase check").click());
  const confirmation = document.querySelector('[data-purchase-check-confirmation]');
  expect(confirmation?.textContent).toContain(locale === "ko-KR" ? "해당 상품을 구매했다고 기록하시겠습니까?" : "Record that you purchased this product?");
  expect(confirmation?.textContent).toContain(locale === "ko-KR" ? "해당 구매 체크는 vitlane의 상품 추천 알고리즘에 반영됩니다." : "This purchase check is used by Vitlane’s product recommendation algorithm.");
  expect(writes).toHaveLength(0);
  await act(async () => bodyButton(locale === "ko-KR" ? "취소" : "Cancel").click());
  expect(writes).toHaveLength(0);
  await act(async () => button(locale === "ko-KR" ? "구매 체크" : "Purchase check").click());
  await act(async () => bodyButton(locale === "ko-KR" ? "확인" : "Confirm").click());
  expect(checked).toBe(true); expect(writes).toHaveLength(1);
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  expect(el.textContent).toContain(locale === "ko-KR" ? "구매함" : "Marked purchased");
  await act(async () => button(locale === "ko-KR" ? "체크 취소" : "Undo").click());
  expect(checked).toBe(false); expect(writes).toHaveLength(2);
  await act(async () => Promise.resolve());
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).toBeNull();
  await act(async () => root.unmount()); el.remove();
});

it("ko-KR: another card's purchase check arrives with its result, so this card does not re-read its state", async () => {
  document.cookie = "vt_locale_choice=ko-KR; Path=/"; window.localStorage.setItem("vitlane.locale.v1", "ko-KR");
  let stateReads = 0;
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (!url.endsWith("/state")) throw new Error(`Unexpected call: ${url}`);
    stateReads++;
    return new Response(JSON.stringify({ variantRef: ref, configurationVersion: 0, purchaseFeedback: { schemaVersion: "vitlane.external-purchase-feedback.v3", version: 1, records: [] } }), { status: 200, headers: { "Content-Type": "application/json" } });
  }));
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  HTMLDialogElement.prototype.close = vi.fn(); HTMLDialogElement.prototype.showModal = vi.fn();
  await act(async () => root.render(<LocaleProvider><AmazonCandidate product={product} curationId="curation" targetId="target" targetTitle="Headsets" interactions={{}} onInteraction={vi.fn()} /></LocaleProvider>));
  await act(async () => Promise.resolve());
  expect(stateReads).toBe(1);
  const feedback = { schemaVersion: "vitlane.external-purchase-feedback.v3", version: 2, records: [{ candidateId: "amazon-card", variantRef: ref, checked: true, version: 1, recordedAt: "2026-09-16T00:00:00Z", evidence: "SELF_REPORTED" }] };
  await act(async () => { window.dispatchEvent(new CustomEvent("vitlane:external-purchase-changed", { detail: { curationId: "curation", feedback } })); });
  expect(stateReads).toBe(1);
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  // 늦게 도착한 옛 값은 무시한다.
  await act(async () => { window.dispatchEvent(new CustomEvent("vitlane:external-purchase-changed", { detail: { curationId: "curation", feedback: { schemaVersion: "vitlane.external-purchase-feedback.v3", version: 1, records: [] } } })); });
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  // 결과 없는 알림은 저장된 선택까지 다시 읽어야 한다.
  await act(async () => { window.dispatchEvent(new Event("vitlane:external-purchase-changed")); });
  await act(async () => Promise.resolve());
  expect(stateReads).toBe(2);
  await act(async () => root.unmount()); el.remove();
});

it("ko-KR: a check on another option still marks the card, and a 409 reloads the state instead of retrying blind", async () => {
  document.cookie = "vt_locale_choice=ko-KR; Path=/"; window.localStorage.setItem("vitlane.locale.v1", "ko-KR");
  let stateReads = 0; const keys: string[] = [];
  const state = () => ({ variantRef: ref, configurationVersion: 0, purchaseFeedback: { version: 1, records: [{ candidateId: "amazon-card", variantRef: { ...ref, asin: "B987654321" }, checked: true, version: 1, recordedAt: "2026-09-14T00:00:00Z", evidence: "SELF_REPORTED" }] } });
  vi.stubGlobal("fetch", vi.fn(async (url: string, options?: RequestInit) => {
    if (url.endsWith("/state")) { stateReads++; return new Response(JSON.stringify(state()), { status: 200, headers: { "Content-Type": "application/json" } }); }
    if (url.endsWith("purchase-check")) { keys.push((options?.headers as Record<string, string>)["Idempotency-Key"]); return new Response(JSON.stringify({ error: { code: "PURCHASE_RECORD_VERSION_CONFLICT", message: "conflict" } }), { status: 409, headers: { "Content-Type": "application/json" } }); }
    throw new Error(`Unexpected provider or Cart call: ${url}`);
  }));
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  HTMLDialogElement.prototype.close = vi.fn(); HTMLDialogElement.prototype.showModal = vi.fn();
  await act(async () => root.render(<LocaleProvider><AmazonCandidate product={product} curationId="curation" targetId="target" targetTitle="Headsets" interactions={{}} onInteraction={vi.fn()} /></LocaleProvider>));
  const button = (label: string) => Array.from(document.body.querySelectorAll("button")).find((b) => b.textContent?.trim() === label)!;
  // The saved option B012345678 is unchecked, so the action still offers a check, but the card carries the signal.
  expect(button("구매 체크")).toBeDefined();
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  expect(stateReads).toBe(1);
  await act(async () => button("구매 체크").click());
  await act(async () => button("확인").click());
  await act(async () => Promise.resolve());
  expect(keys).toHaveLength(1);
  expect(stateReads).toBe(2);
  expect(el.textContent).toContain("현재 상태를 다시 불러왔습니다");
  // The conflict cleared the pending command: the next attempt is a new command, not a replay.
  await act(async () => button("구매 체크").click());
  await act(async () => button("확인").click());
  await act(async () => Promise.resolve());
  expect(keys).toHaveLength(2); expect(keys[0]).not.toBe(keys[1]);
  await act(async () => root.unmount()); el.remove();
});

for (const locale of ["en-US", "ko-KR"] as const) it(`${locale}: Amazon reactions share the Variant API, preserve state on failure and reload, and keep unknown prices unknown`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`; window.localStorage.setItem("vitlane.locale.v1", locale);
  type Reaction = { pinned: boolean; sentiment: "NONE" | "LIKE" | "DISLIKE" };
  let saved: Record<string, Reaction> = {}; let fail = false; let writes = 0;
  vi.stubGlobal("fetch", vi.fn(async (url: string, options?: RequestInit) => {
    if (url.endsWith("/interaction")) {
      expect(url).toContain("/variants/B012345678/");
      const body = JSON.parse(options?.body as string);
      expect(body.likedSnapshot.priceUnknown).toBe(true);
      expect(body.likedSnapshot.productUrl).toBe("https://www.amazon.com/dp/B012345678");
      if (fail) return new Response("{}", { status: 503 });
      saved = { ...saved, "amazon-card::B012345678": { pinned: body.pinned, sentiment: body.sentiment } }; writes++;
      return new Response(null, { status: 204 });
    }
    if (url.endsWith("/variant-pages")) return new Response(JSON.stringify({ relationToken: "a".repeat(64), rows: [{variantId: ref.asin, title: "Black", selectedOptions: [], available: true}], pagination: {pageSize:20,hasNext:false} }), {status:200});
    if (!url.endsWith("/state")) throw new Error(`Unexpected provider or Cart call: ${url}`);
    return new Response(JSON.stringify({ variantRef: ref, configurationVersion: 0, purchaseFeedback: { version: 0, records: [] } }), { status: 200 });
  }));
  function Harness() {
    const [values, setValues] = useState(saved);
    const reaction = values["amazon-card::B012345678"];
    return <AmazonCandidate product={product} curationId="curation" targetId="target" targetTitle="Headsets" interactions={values}
      signals={{ pinned: reaction?.pinned ?? false, liked: reaction?.sentiment === "LIKE", disliked: reaction?.sentiment === "DISLIKE" }}
      onInteraction={async (key, value, likedSnapshot) => {
        const [candidateId, variantId] = key.split("::");
        await saveCatalogInteraction({ curationId: "curation", candidateId, variantId, ...value, likedSnapshot });
        setValues((old) => ({ ...old, [key]: value }));
      }} />;
  }
  const el = document.createElement("div"); document.body.append(el); let root = createRoot(el);
  HTMLDialogElement.prototype.close = vi.fn(); HTMLDialogElement.prototype.showModal = vi.fn();
  const mount = () => act(async () => root.render(<LocaleProvider><Harness /></LocaleProvider>));
  await mount();
  await act(async () => el.querySelector<HTMLButtonElement>(".vt-candidate-card__hit-area")!.click());
  const button = (en: string, ko: string) => Array.from(document.body.querySelectorAll("button")).find((b) => b.textContent?.trim() === (locale === "ko-KR" ? ko : en))!;
  await act(async () => button("Pin", "Pin").click());
  await act(async () => button("Like", "좋아요").click());
  expect(saved["amazon-card::B012345678"]).toEqual({ pinned: true, sentiment: "LIKE" });
  expect(button("Like", "좋아요").getAttribute("aria-pressed")).toBe("true");
  fail = true;
  await act(async () => button("Dislike", "싫어요").click());
  expect(button("Like", "좋아요").getAttribute("aria-pressed")).toBe("true");
  expect(writes).toBe(2); fail = false;
  await act(async () => root.unmount()); root = createRoot(el); await mount();
  await act(async () => el.querySelector<HTMLButtonElement>(".vt-candidate-card__hit-area")!.click());
  expect(button("Unpin", "Pin 해제").getAttribute("aria-pressed")).toBe("true");
  await act(async () => button("Dislike", "싫어요").click());
  expect(saved["amazon-card::B012345678"]).toEqual({ pinned: true, sentiment: "DISLIKE" });
  await act(async () => button("Unpin", "Pin 해제").click());
  expect(saved["amazon-card::B012345678"].pinned).toBe(false);
  await act(async () => root.unmount()); el.remove();
});

it("uses workspace Candidate hydration without a duplicate Amazon lookup", async () => {
  const calls: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    calls.push(url);
    if (!url.endsWith("/state")) throw new Error("Duplicate initial provider lookup");
    return new Response(JSON.stringify({ variantRef: ref, configurationVersion: 0, purchaseFeedback: { version: 0, records: [] } }), { status: 200 });
  }));
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  HTMLDialogElement.prototype.close = vi.fn(); HTMLDialogElement.prototype.showModal = vi.fn();
  const render = (p: LiveCatalogProduct) => act(async () => root.render(<LocaleProvider><AmazonCandidate product={p} curationId="curation" targetId="target" targetTitle="Headphones" interactions={{}} onInteraction={vi.fn()} /></LocaleProvider>));
  await render({ ...product, title: "", variantObservation: undefined });
  expect(calls).toHaveLength(1);
  await render({ ...product, title: "Hydrated by workspace", hydration: { status: "READY", retryable: false } });
  expect(el.textContent).toContain("Hydrated by workspace");
  expect(el.querySelector("article")?.getAttribute("aria-busy")).toBeNull();
  expect(calls).toHaveLength(1);
  await act(async () => root.unmount()); el.remove();
});

for (const locale of ["en-US", "ko-KR"] as const) it(`${locale}: shares detail layout, separates draft/reaction/save, and purchases the saved exact option`, async () => {
  document.cookie = `vt_locale_choice=${locale}; Path=/`; window.localStorage.setItem("vitlane.locale.v1", locale);
  let selectedASIN = ref.asin; let configVersion = 0; let feedbackVersion = 0; let checked = false;
  const writes: Array<{ url: string; body: Record<string, unknown> }> = [];
  const variants = [{variantId: ref.asin, title: "Black", selectedOptions: [{name:"Color",value:"Black"}], available:true},
    {variantId:"B987654321",title:"Blue",selectedOptions:[{name:"Color",value:"Blue"}],available:false}];
  const feedback = () => ({ version: feedbackVersion, records: feedbackVersion ? [{variantRef:{...ref,asin:selectedASIN},checked,version:feedbackVersion}] : [] });
  const state = () => ({ variantRef:{...ref,asin:selectedASIN},configurationVersion:configVersion,purchaseFeedback:feedback() });
  const detail = (asin: string) => ({ ...product, variantObservation: { ...product.variantObservation!, variantRef:{...ref,asin}, productUrl:`https://www.amazon.com/dp/${asin}` } });
  vi.stubGlobal("fetch", vi.fn(async (url: string, options?: RequestInit) => {
    expect(url).not.toMatch(/\/cart|checkout/);
    if (url.endsWith("/state")) return new Response(JSON.stringify(state()), {status:200});
    if (url.endsWith("/variant-pages")) return new Response(JSON.stringify({relationToken:"a".repeat(64),rows:variants,pagination:{pageSize:20,hasNext:false},observedAt:"2026-09-10T00:00:00Z"}),{status:200});
    if (url.endsWith("/resolve")) return new Response(JSON.stringify({product:detail(JSON.parse(options!.body as string).variantRef.asin)}),{status:200});
    if (url.endsWith("/configuration")) {
      const body=JSON.parse(options!.body as string);expect(body.expectedVersion).toBe(configVersion);expect(body.relationToken).toBe("a".repeat(64));
      writes.push({url,body});selectedASIN=body.variantId;configVersion++;return new Response(null,{status:204});
    }
    if (url.endsWith("/purchase-check")) {
      const body=JSON.parse(options!.body as string);expect(body.variantRef.asin).toBe(selectedASIN);expect(body.expectedVersion).toBe(feedbackVersion);
      writes.push({url,body});checked=body.checked;feedbackVersion++;return new Response(JSON.stringify(feedback()),{status:200});
    }
    throw new Error(`Unexpected request ${url}`);
  }));
  const react = vi.fn(async () => undefined); const opened = vi.spyOn(window,"open").mockImplementation(() => null);
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  const btn=(en:string,ko:string)=>Array.from(document.body.querySelectorAll<HTMLButtonElement>("button")).find(b=>b.textContent?.trim()===(locale==="ko-KR"?ko:en))!;
  const open = () => act(async()=>el.querySelector<HTMLButtonElement>(".vt-candidate-card__hit-area")!.click());
  const blue = () => document.querySelectorAll<HTMLButtonElement>('[role="radio"]')[1];
  try {
    await act(async()=>root.render(<LocaleProvider><AmazonCandidate product={product} curationId="curation" targetId="target" targetTitle="Headsets" interactions={{}} onInteraction={react}/></LocaleProvider>));
    await open(); expect(document.querySelector('.candidate-detail [data-source="AMAZON"]')).not.toBeNull();
    expect(document.querySelectorAll(".candidate-detail__section")).toHaveLength(5);
    expect(document.querySelectorAll('.candidate-detail .vt-disclosure__trigger[aria-expanded="true"]')).toHaveLength(5);
    const footer = document.querySelector('.catalog-ui-candidate-modal__footer-actions[data-purchase-kind="EXTERNAL"]')!;
    expect(footer.firstElementChild?.classList.contains("candidate-detail__save-action")).toBe(true);
    expect(footer.lastElementChild?.classList.contains("candidate-detail__external-actions")).toBe(true);
    expect(footer.lastElementChild?.querySelectorAll("button")).toHaveLength(2);
    await act(async()=>blue().click()); expect(writes).toHaveLength(0);expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    expect(document.querySelector(".catalog-ui-candidate-modal__identity")?.textContent).toContain("Blue");
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain("$0.00");
    await act(async()=>btn("Like","좋아요").click());
    expect(react).toHaveBeenCalledWith("amazon-card::B987654321",{pinned:false,sentiment:"LIKE"},expect.objectContaining({variantTitle:"Blue",priceUnknown:true}),"a".repeat(64));
    expect(selectedASIN).toBe(ref.asin);
    await act(async()=>document.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true})));
    await open(); expect(document.querySelectorAll('[role="radio"]')[0].getAttribute("aria-checked")).toBe("true");
    await act(async()=>blue().click());await act(async()=>btn("Save option","옵션 저장").click());
    expect(selectedASIN).toBe("B987654321"); expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(el.textContent).toContain("Blue");
    await act(async()=>btn("External purchase","외부 구매").click());expect(opened).toHaveBeenCalledWith("https://www.amazon.com/dp/B987654321","_blank","noopener,noreferrer");
    await act(async()=>btn("Purchase check","구매 체크").click());
    await act(async()=>btn("Confirm","확인").click());expect(checked).toBe(true);
    await act(async()=>btn("Undo","체크 취소").click());expect(checked).toBe(false);
  } finally { await act(async()=>root.unmount()); el.remove();opened.mockRestore(); }
});

it("워크스페이스가 준 상태를 쓰면 Amazon 카드가 상태를 따로 읽지 않는다", async () => {
  document.cookie = "vt_locale_choice=ko-KR; Path=/"; window.localStorage.setItem("vitlane.locale.v1", "ko-KR");
  let reads = 0; let stale = 0;
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (!url.endsWith("state")) throw new Error(`Unexpected call: ${url}`);
    reads++;
    return new Response(JSON.stringify({ variantRef: ref, configurationVersion: 0, purchaseFeedback: { version: 0, records: [] } }), { status: 200, headers: { "Content-Type": "application/json" } });
  }));
  const cardState = { variantRef: ref, configurationVersion: 2, purchaseFeedback: { schemaVersion: "vitlane.external-purchase-feedback.v4", version: 5, records: [{ variantRef: ref, candidateId: "amazon-card", checked: true, version: 3, recordedAt: "2026-09-16T00:00:00Z", evidence: "SELF_REPORTED" as const }] } };
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  HTMLDialogElement.prototype.close = vi.fn(); HTMLDialogElement.prototype.showModal = vi.fn();
  await act(async () => root.render(<LocaleProvider><AmazonCandidate product={product} curationId="curation" targetId="target" targetTitle="Headsets" interactions={{}} cardState={cardState} onStateStale={() => { stale++; }} onInteraction={vi.fn()} /></LocaleProvider>));
  await act(async () => Promise.resolve());
  // 화면을 열 때 카드 수만큼 조회하지 않는다.
  expect(reads).toBe(0);
  expect(el.querySelector(".vt-candidate-card__signal.is-purchased")).not.toBeNull();
  expect(Array.from(el.querySelectorAll("button")).some((b) => b.textContent === "체크 취소")).toBe(true);
  // 결과 없는 알림에도 카드가 아니라 한 번의 워크스페이스 읽기를 요청한다.
  await act(async () => { window.dispatchEvent(new Event("vitlane:external-purchase-changed")); });
  await act(async () => Promise.resolve());
  expect(reads).toBe(0); expect(stale).toBe(1);
  await act(async () => root.unmount()); el.remove();
});
