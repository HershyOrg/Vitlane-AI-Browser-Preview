// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes, useParams } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgencyOrderCapability, OrderSheet } from "../infra/agencyOrderApi";
import { getAgencyOrderCapability } from "../infra/agencyOrderApi";
import { OrderSheetPage } from "./OrderSheetPage";
import { LocaleProvider } from "../../../shared/i18n";

function capabilityFixture(options: { sandbox?: "READY" | "UNAVAILABLE"; live?: "READY" | "PAUSED" | "UNAVAILABLE" } = {}) {
  const rail = (method: "TVITUSD" | "PAYPAL_SANDBOX" | "PAYPAL_LIVE", state: "READY" | "PAUSED" | "UNAVAILABLE"): AgencyOrderCapability["paymentRails"]["paypalLive"] => ({
    state, orderIssueState: state, paymentInitiationState: state,
    paymentMethod: method, providerEnvironment: method === "TVITUSD" ? "TESTNET" : method === "PAYPAL_SANDBOX" ? "SANDBOX" : "LIVE",
    asset: method === "TVITUSD" ? "TVITUSD" : "USD",
    economicEffect: method === "PAYPAL_LIVE" ? "REAL_MONEY" : "NO_REAL_VALUE",
  });
  return {
    schemaVersion: "vitlane.agency-order-capability.v2" as const,
    capability: {
      state: "READY" as const, checkoutProvider: "STUB" as const, capabilityRevision: 8,
      paymentRails: {
        tvitusd: rail("TVITUSD", "READY"),
        paypalSandbox: rail("PAYPAL_SANDBOX", options.sandbox ?? "UNAVAILABLE"),
        paypalLive: rail("PAYPAL_LIVE", options.live ?? "UNAVAILABLE"),
      },
    },
  };
}

vi.mock("../infra/agencyOrderApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../infra/agencyOrderApi")>()),
  getAgencyOrderCapability: vi.fn().mockResolvedValue(capabilityFixture()),
}));

describe("AgencyOrder OrderSheetPage", () => {
  let container: HTMLDivElement | undefined;
  let root: Root | undefined;

  afterEach(async () => {
    if (root) await act(async () => root?.unmount());
    container?.remove();
    root = undefined;
    container = undefined;
    window.sessionStorage.clear();
    vi.unstubAllGlobals();
  });

  it("만료된 주문서는 CTA를 잠그고 새 주문서 재생성 경로를 제공한다", async () => {
    let creations = 0;
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(async () => {
      creations += 1;
      return jsonResponse({
        schemaVersion: "vitlane.order-sheet.v1",
        orderSheet: sheetFixture(
          creations === 1
            ? { expiresAt: new Date(Date.now() - 1000).toISOString() }
            : { id: "sheet-renewed" },
        ),
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mount();
    await settle();

    expect(view.textContent).toContain("주문서가 만료되었습니다.");
    expect(
      (button(view, "주문하기") as HTMLButtonElement).disabled,
    ).toBe(true);

    await act(async () => button(view, "새 주문서 만들기").click());
    await settle();

    expect(creations).toBe(2);
    expect(view.textContent).not.toContain("주문서가 만료되었습니다.");
  });

  it.each([
    {
      locale: "ko-KR",
      reasonCode: "VARIANT_UNAVAILABLE",
      expected: "현재 구매할 수 없는 상품입니다: Classic Rim Dinnerware Set. 장바구니에서 제거하거나 다른 옵션으로 바꾼 뒤 다시 시도해 주세요.",
    },
    {
      locale: "en-US",
      reasonCode: "VARIANT_NOT_RESOLVED",
      expected: "We couldn't confirm that Classic Rim Dinnerware Set is still available, so it cannot be ordered right now. Remove it from your cart or choose another option.",
    },
  ])("$reasonCode 주문 준비 오류가 $locale에서 상품명과 복구 행동을 안내한다", async ({ locale, reasonCode, expected }) => {
    document.cookie = `vt_locale_choice=${locale}; Path=/`;
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({
      error: {
        code: "CONFLICT",
        message: "The selected product cannot be ordered.",
        reasonCode,
        retryable: reasonCode !== "VARIANT_UNAVAILABLE",
        requestId: "request-1",
        itemTitle: "Classic Rim Dinnerware Set",
      },
    }, 409)));

    const view = await mount();
    await settle();

    expect(view.textContent).toContain(expected);
    expect(view.textContent).not.toContain(`(${reasonCode})`);
  });

  it("PayPal Live와 Sandbox가 비활성이어도 세 rail을 구분해 보이고 선택을 차단한다", async () => {
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(async (input) => {
      if (String(input).endsWith("/agency-order-capability")) {
        return jsonResponse(capabilityFixture());
      }
      return jsonResponse({
        schemaVersion: "vitlane.order-sheet.v1",
        orderSheet: sheetFixture({ state: "EDITING" }),
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mount();
    await settle();

    expect(view.textContent).toContain("Pilot 일시 중단");
    expect(view.textContent).toContain("PayPal Sandbox · 테스트 전용");
    expect((radio(view, "PayPal Live") as HTMLButtonElement).disabled).toBe(true);
    expect((radio(view, "PayPal Sandbox") as HTMLButtonElement).disabled).toBe(true);
    // 세션 생성 + capability 확인 외의 preflight/발행 요청은 없어야 한다.
    const calledPaths = fetchMock.mock.calls.map((call) => String(call[0]));
    expect(calledPaths.some((path) => path.includes("/preflight"))).toBe(false);
    expect(calledPaths.some((path) => path.includes("/issue"))).toBe(false);
  });

  it("PayPal 예상 수수료는 Merchant Order별 5.4% 올림과 USD 0.30을 각각 합산한다", async () => {
    vi.mocked(getAgencyOrderCapability).mockResolvedValueOnce(capabilityFixture({ sandbox: "READY" }));
    const first = sheetFixture();
    const firstCheckout = first.merchantCheckouts[0];
    if (!firstCheckout) throw new Error("checkout fixture missing");
    const multiShop = sheetFixture({
      lines: [
        { ...first.lines[0]!, lineId: "line-1", shopDomain: "first.example", lineSubtotal: { amountMinor: 101, currency: "USD" }, unitPrice: { amountMinor: 101, currency: "USD" } },
        { ...first.lines[0]!, lineId: "line-2", sourceCartItemId: "item-2", candidateId: "candidate-2", shopDomain: "second.example", lineSubtotal: { amountMinor: 101, currency: "USD" }, unitPrice: { amountMinor: 101, currency: "USD" } },
      ],
      merchantCheckouts: [
        { ...firstCheckout, merchantId: "merchant-1", shopDomain: "first.example", authoritativeTotal: { amountMinor: 101, currency: "USD" }, deliveryGroups: [{ ...firstCheckout.deliveryGroups[0]!, id: "group-1", lineRefs: ["line-1"] }] },
        { ...firstCheckout, merchantId: "merchant-2", shopDomain: "second.example", authoritativeTotal: { amountMinor: 101, currency: "USD" }, deliveryGroups: [{ ...firstCheckout.deliveryGroups[0]!, id: "group-2", lineRefs: ["line-2"] }] },
      ],
      passThroughTotal: { amountMinor: 202, currency: "USD" },
      agencyFee: { amountMinor: 2, currency: "USD" },
      customerPayableTotal: { amountMinor: 204, currency: "USD" },
    });
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({
      schemaVersion: "vitlane.order-sheet.v1",
      orderSheet: multiShop,
    })));

    const view = await mount();
    await settle();
    await act(async () => radio(view, "PayPal Sandbox").click());
    await settle();

    // ceil($1.01 × 5.4%) + $0.30 = $0.36 for each of two MOs.
    expect(view.textContent).toContain("PayPal용 Vitlane 수수료 (Merchant Order마다 5.4% + USD 0.30)$0.72");
  });

  it("PayPal rail이 활성인 배포는 PAYPAL_SANDBOX preflight와 발행으로 진행한다", async () => {
    vi.mocked(getAgencyOrderCapability).mockResolvedValueOnce(capabilityFixture({ sandbox: "READY" }));
    const requested: string[] = [];
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(async (input, init) => {
      const path = String(input);
      requested.push(path);
      if (path.endsWith("/agency-order-capability")) {
        return jsonResponse(capabilityFixture({ sandbox: "READY" }));
      }
      if (path.endsWith("/preflight")) {
        expect(JSON.parse(String(init?.body)).paymentMethod).toBe("PAYPAL_SANDBOX");
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({
            state: "READY", version: 3, paymentSelection: "PAYPAL_SANDBOX",
            displayedSnapshotHash: "sha256:displayed",
            passThroughTotal: { amountMinor: 1000, currency: "USD" },
            agencyFee: { amountMinor: 84, currency: "USD" },
            customerPayableTotal: { amountMinor: 1084, currency: "USD" },
          }),
        });
      }
      if (path.endsWith("/issue")) {
        return jsonResponse({
          schemaVersion: "vitlane.agency-order.v1",
          agencyOrder: { id: "order-paypal-1" },
          paymentInstruction: { id: "instruction-1" },
        }, 201);
      }
      return jsonResponse({
        schemaVersion: "vitlane.order-sheet.v1",
        orderSheet: sheetFixture({ state: "EDITING" }),
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => radio(view, "PayPal Sandbox").click());
    await settle();
    // 활성 rail은 SANDBOX 표기와 실제 진행 CTA를 보여준다.
    expect(view.textContent).toContain("PayPal Sandbox · 테스트 전용");
    await act(async () => button(view, "주문하기").click());
    await settle();
    await act(async () => button(view, "$10.84로 주문 확정").click());
    await settle();

    expect(requested.some((path) => path.includes("/preflight"))).toBe(true);
    expect(requested.some((path) => path.includes("/issue"))).toBe(true);
    expect(view.textContent).toContain("PAYMENT ROUTE order-paypal-1");
  });

  it("Live issue gate가 열린 배포는 경고를 표시하고 PAYPAL_LIVE만 제출한다", async () => {
    vi.mocked(getAgencyOrderCapability).mockResolvedValueOnce(capabilityFixture({ live: "READY" }));
    const submittedMethods: string[] = [];
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(async (input, init) => {
      const path = String(input);
      if (path.endsWith("/preflight")) {
        submittedMethods.push(JSON.parse(String(init?.body)).paymentMethod);
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({
            state: "READY", version: 3, paymentSelection: "PAYPAL_LIVE",
            displayedSnapshotHash: "sha256:live-displayed",
            passThroughTotal: { amountMinor: 1000, currency: "USD" },
            agencyFee: { amountMinor: 84, currency: "USD" },
            customerPayableTotal: { amountMinor: 1084, currency: "USD" },
          }),
        });
      }
      if (path.endsWith("/issue")) {
        return jsonResponse({
          schemaVersion: "vitlane.agency-order.v1",
          agencyOrder: { id: "order-live-1" },
          paymentInstruction: { id: "instruction-live-1" },
        }, 201);
      }
      return jsonResponse({
        schemaVersion: "vitlane.order-sheet.v1",
        orderSheet: sheetFixture({ state: "EDITING" }),
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => radio(view, "PayPal Live").click());
    const liveAcknowledgement = view.querySelector("#paypal-live-acknowledgement") as HTMLElement;
    expect(liveAcknowledgement.classList.contains("agency-order-procurement-authorization__checkbox")).toBe(true);
    expect(liveAcknowledgement.closest("label")?.classList.contains("agency-order-procurement-authorization__consent")).toBe(true);
    await act(async () => liveAcknowledgement.click());
    expect(view.textContent).toContain("PayPal Live · 실제 USD 승인");
    expect(view.textContent).toContain("PayPal LIVE · 실제 금액");
    await act(async () => button(view, "주문하기").click());
    await settle();
    await act(async () => button(view, "$10.84로 주문 확정").click());
    await settle();

    expect(submittedMethods).toEqual(["PAYPAL_LIVE"]);
    expect(view.textContent).toContain("PAYMENT ROUTE order-live-1");
  });

  it("provider warning은 읽기 전용으로 보이고 별도 답변 없이 발행한다", async () => {
    const ready = sheetFixture({
      state: "READY", paymentSelection: "TVITUSD", displayedSnapshotHash: "sha256:provider-notice",
    });
    ready.merchantCheckouts[0].providerNotices = [{
      source: "MESSAGE", type: "warning", severity: "requires_buyer_review", code: "review_return_policy",
      text: "Review returns before purchase.", presentation: "NOTICE",
      audience: "CUSTOMER_AND_OPERATOR", registered: false,
    }];
    let issueBody: unknown;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.endsWith("/order-sheets")) {
        return jsonResponse({ schemaVersion: "vitlane.order-sheet.v1", orderSheet: ready }, 201);
      }
      if (path.endsWith("/issue")) {
        issueBody = JSON.parse(String(init?.body));
        return jsonResponse({
          schemaVersion: "vitlane.agency-order.v1",
          agencyOrder: { id: "order-provider-notice" },
          paymentInstruction: { id: "instruction-provider-notice" },
        }, 201);
      }
      throw new Error(`unexpected request ${path}`);
    }));
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    expect(view.textContent).toContain("판매처 안내");
    expect(view.textContent).toContain("Review returns before purchase.");
    expect(button(view, "확정 주문서 작성하고 tVITUSD로 결제").disabled).toBe(false);
    await act(async () => button(view, "확정 주문서 작성하고 tVITUSD로 결제").click());
    await settle();

    expect((issueBody as { procurementApproval: Record<string, unknown> }).procurementApproval).not.toHaveProperty("additionalRequirementAnswers");
    expect(view.textContent).toContain("PAYMENT ROUTE order-provider-notice");
  });

  it("이미 소비된 OrderSheet는 기존 AgencyOrder 결제 화면으로 즉시 복구한다", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({
      schemaVersion: "vitlane.order-sheet.v1",
      orderSheet: sheetFixture({
        state: "CONSUMED",
        issuedAgencyOrderId: "order-existing",
      }),
    }, 201));
    vi.stubGlobal("fetch", fetchMock);

    const view = await mount();
    await settle();

    expect(view.textContent).toContain("PAYMENT ROUTE order-existing");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("제출 중 소비된 OrderSheet 오류도 기존 AgencyOrder 결제로 복구한다", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).endsWith("/order-sheets")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ state: "EDITING" }),
        }, 201);
      }
      if (String(input).endsWith("/preflight")) {
        return jsonResponse({
          error: {
            code: "ORDER_SHEET_ALREADY_CONSUMED",
            message: "이미 생성된 AgencyOrder 결제를 계속해 주세요.",
            agencyOrderId: "order-race",
          },
        }, 409);
      }
      throw new Error(`unexpected request ${String(input)}`);
    }));
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => button(view, "주문하기").click());
    await settle();

    expect(view.textContent).toContain("PAYMENT ROUTE order-race");
  });

  it("계정 기본 배송지가 없어도 OrderSheet에서 주소를 입력하고 배송 옵션을 탐색한다", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      calls.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/order-sheets")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({
            shippingAddress: undefined,
            shippingAddressInput: { ...emptyAddress },
            shippingAddressSource: "EMPTY",
            merchantCheckouts: [],
          }),
        }, 201);
      }
      if (path.endsWith("/shipping-address")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ version: 3 }),
        });
      }
      throw new Error(`unexpected request ${path}`);
    }));
    const view = await mount();
    await settle();

    expect(view.textContent).toContain("이 주문의 배송지");
    expect(button(view, "배송지를 먼저 확인해 주세요").disabled).toBe(true);
    await fillShipping(view);
    await act(async () => button(view, "이 배송지로 배송 옵션 확인").click());
    await settle();

    expect(calls).toEqual([
      "POST /api/v1/curations/curation-1/order-sheets",
      "PUT /api/v1/order-sheets/sheet-1/shipping-address",
    ]);
    expect(view.textContent).toContain("계정 기본 배송지는 변경하지 않았습니다.");
  });

  it("명시적으로 선택한 경우에만 OrderSheet 주소를 계정 기본 배송지에도 저장한다", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      calls.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/order-sheets")) return jsonResponse({
        schemaVersion: "vitlane.order-sheet.v1",
        orderSheet: sheetFixture({
          shippingAddress: undefined, shippingAddressInput: { ...emptyAddress },
          shippingAddressSource: "EMPTY", merchantCheckouts: [],
        }),
      }, 201);
      if (path.endsWith("/shipping-address")) return jsonResponse({
        schemaVersion: "vitlane.order-sheet.v1", orderSheet: sheetFixture({ version: 3 }),
      });
      if (path === "/api/v1/account/shipping-profiles/default") return jsonResponse({ shippingProfile: { id: "profile-1" } }, 201);
      throw new Error(`unexpected request ${path}`);
    }));
    const view = await mount();
    await settle();
    await fillShipping(view);
    await act(async () => (view.querySelector("#order-shipping-save-default") as HTMLButtonElement).click());
    await act(async () => button(view, "이 배송지로 배송 옵션 확인").click());
    await settle();

    expect(calls).toEqual([
      "POST /api/v1/curations/curation-1/order-sheets",
      "PUT /api/v1/order-sheets/sheet-1/shipping-address",
      "PUT /api/v1/account/shipping-profiles/default",
    ]);
    expect(view.textContent).toContain("계정 기본 배송지에도 저장했습니다.");
  });

  it("tVITUSD는 배송 선택 저장, UCP preflight, 원자 발행 뒤 결제 화면으로 이동한다", async () => {
    const calls: Array<{ path: string; method: string; body: unknown }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      const method = init?.method ?? "GET";
      calls.push({
        path,
        method,
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      });
      if (path.endsWith("/order-sheets")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ state: "DELIVERY_SELECTION_REQUIRED" }),
        });
      }
      if (path.endsWith("/delivery-selections")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ state: "EDITING", version: 2 }),
        });
      }
      if (path.endsWith("/preflight")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: reviewReadySheet({
            state: "READY", version: 3, paymentSelection: "TVITUSD", displayedSnapshotHash: "sha256:displayed",
          }),
        });
      }
      if (path.endsWith("/issue")) {
        return jsonResponse({
          schemaVersion: "vitlane.agency-order.v1",
          agencyOrder: { id: "order-1" },
          paymentInstruction: { id: "instruction-1" },
        }, 201);
      }
      throw new Error(`unexpected request ${method} ${path}`);
    }));
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => button(view, "주문하기").click());
    await settle();

    expect(view.textContent).toContain("읽기 전용 후속 정보도 반환했으며");
    expect(view.textContent).toContain("같은 Merchant Order 안의 상품·unit·line을 따로 취소하거나 환불할 수 없습니다");
    expect(view.textContent).toContain("배분된 상품·배송비·세금·Vitlane 수수료를 모두 반환합니다");
    expect(view.textContent).toContain("단순 변심 반품은 지원하지 않습니다");
    expect(view.textContent).toContain("미도착·오배송·하자·조달 실패 등 정당한 사유");
    expect(calls.map(({ path, method }) => `${method} ${path}`)).toEqual([
      "POST /api/v1/curations/curation-1/order-sheets",
      "PUT /api/v1/order-sheets/sheet-1/delivery-selections",
      "POST /api/v1/order-sheets/sheet-1/preflight",
    ]);

    await act(async () => button(view, "$11.11로 주문 확정").click());
    await settle();

    expect(calls.map(({ path, method }) => `${method} ${path}`)).toEqual([
      "POST /api/v1/curations/curation-1/order-sheets",
      "PUT /api/v1/order-sheets/sheet-1/delivery-selections",
      "POST /api/v1/order-sheets/sheet-1/preflight",
      "POST /api/v1/order-sheets/sheet-1/issue",
    ]);
    expect(calls[1]?.body).toEqual({
      expectedVersion: 1,
      selections: [{ shopDomain: "shop.example", groupId: "group-1", optionId: "standard" }],
    });
    expect(calls[2]?.body).toEqual({ expectedVersion: 2, paymentMethod: "TVITUSD" });
    expect(calls[3]?.body).toMatchObject({
      expectedVersion: 3,
      displayedSnapshotHash: "sha256:displayed",
    });
    expect(view.textContent).toContain("PAYMENT ROUTE order-1");
  });

  // 운영정합 5차 C1: 화면이 보여준 총액과 검증 결과가 같으면 preflight 후 같은
  // 클릭에서 발행한다 — 종전엔 성공해도 무언 반환해 "첫 클릭 실패"로 읽혔다.
  it("배송 재선택 후 총액이 그대로면 한 클릭으로 검증·발행까지 간다", async () => {
    const ready = sheetFixture({
      state: "READY", paymentSelection: "TVITUSD", displayedSnapshotHash: "sha256:v1",
    });
    ready.merchantCheckouts[0].deliveryGroups[0].options.push(
      { id: "express", title: "Express", amountMinor: 0, currency: "USD" },
    );
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      calls.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/order-sheets")) {
        return jsonResponse({ schemaVersion: "vitlane.order-sheet.v1", orderSheet: ready }, 201);
      }
      if (path.endsWith("/delivery-selections")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ state: "EDITING", version: 2 }),
        });
      }
      if (path.endsWith("/preflight")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({
            state: "READY", version: 3, paymentSelection: "TVITUSD",
            displayedSnapshotHash: "sha256:v3",
          }),
        });
      }
      if (path.endsWith("/issue")) {
        return jsonResponse({
          schemaVersion: "vitlane.agency-order.v1",
          agencyOrder: { id: "order-oneclick" },
          paymentInstruction: { id: "instruction-1" },
        }, 201);
      }
      throw new Error(`unexpected request ${path}`);
    }));
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => radio(view, "Express").click());
    await act(async () => button(view, "확정 주문서 작성하고 tVITUSD로 결제").click());
    await settle();

    expect(calls.filter((call) => call.includes("/issue"))).toHaveLength(1);
    expect(view.textContent).toContain("PAYMENT ROUTE order-oneclick");
  });

  it("검증에서 총액이 바뀌면 발행을 멈추고 확정 금액을 안내한 뒤, 재클릭으로 발행한다", async () => {
    const ready = sheetFixture({
      state: "READY", paymentSelection: "TVITUSD", displayedSnapshotHash: "sha256:v1",
    });
    ready.merchantCheckouts[0].deliveryGroups[0].options.push(
      { id: "express", title: "Express", amountMinor: 0, currency: "USD" },
    );
    const repriced = sheetFixture({
      state: "READY", version: 3, paymentSelection: "TVITUSD",
      displayedSnapshotHash: "sha256:v3",
      customerPayableTotal: { amountMinor: 1211, currency: "USD" },
    });
    repriced.merchantCheckouts[0].deliveryGroups[0].options.push(
      { id: "express", title: "Express", amountMinor: 0, currency: "USD" },
    );
    repriced.merchantCheckouts[0].deliveryGroups[0].selectedOptionRef = "express";
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      calls.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/order-sheets")) {
        return jsonResponse({ schemaVersion: "vitlane.order-sheet.v1", orderSheet: ready }, 201);
      }
      if (path.endsWith("/delivery-selections")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ state: "EDITING", version: 2 }),
        });
      }
      if (path.endsWith("/preflight")) {
        return jsonResponse({ schemaVersion: "vitlane.order-sheet.v1", orderSheet: repriced });
      }
      if (path.endsWith("/issue")) {
        return jsonResponse({
          schemaVersion: "vitlane.agency-order.v1",
          agencyOrder: { id: "order-repriced" },
          paymentInstruction: { id: "instruction-1" },
        }, 201);
      }
      throw new Error(`unexpected request ${path}`);
    }));
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => radio(view, "Express").click());
    await act(async () => button(view, "확정 주문서 작성하고 tVITUSD로 결제").click());
    await settle();

    // 무언 중단 금지 — 확정 금액과 재확인 안내가 보이고 발행은 없다.
    expect(view.textContent).toContain("$11.11에서 $12.11로 바뀌었습니다");
    expect(view.textContent).toContain("한 번 더 눌러 주문을 확정");
    expect(view.querySelector(".agency-order-error")).toBeNull();
    expect(calls.some((call) => call.includes("/issue"))).toBe(false);

    await act(async () => button(view, "$12.11로 주문 확정").click());
    await settle();
    expect(calls.filter((call) => call.includes("/issue"))).toHaveLength(1);
    expect(view.textContent).toContain("PAYMENT ROUTE order-repriced");
  });

  it("배송지 하드 오류는 CUSTOMER_CORRECTION으로 안내하고 주소 폼을 다시 연다", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/order-sheets")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ state: "EDITING" }),
        }, 201);
      }
      if (path.endsWith("/preflight")) {
        const blocked = sheetFixture({
          state: "BLOCKED", blockReason: "CHECKOUT_CUSTOMER_CORRECTION_REQUIRED", version: 2,
          paymentSelection: "TVITUSD",
        });
        blocked.merchantCheckouts = blocked.merchantCheckouts.map((checkout) => ({
          ...checkout,
          providerStatus: "requires_escalation",
          quoteReadiness: "CONFIRMED" as const,
          procurementHandling: "CUSTOMER_CORRECTION" as const,
          providerNotices: [{ source: "MESSAGE" as const, type: "error", severity: "requires_buyer_input", code: "address_undeliverable", presentation: "INTERNAL" as const, audience: "OPERATOR" as const, registered: true }],
        }));
        return jsonResponse({ schemaVersion: "vitlane.order-sheet.v1", orderSheet: blocked });
      }
      throw new Error(`unexpected request ${path}`);
    }));
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => button(view, "주문하기").click());
    await settle();

    expect(view.textContent).toContain("현재 배송정보를 사용할 수 없습니다");
    expect(view.textContent).toContain("해당 판매처: shop.example");
    expect(view.textContent).not.toContain("address_undeliverable");
    expect((view.querySelector("#order-shipping-line-1") as HTMLInputElement).disabled).toBe(false);
  });

  it("extension과 redirect가 함께 와도 확정 금액과 운영자 후속 안내로 발행한다", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/order-sheets")) {
        return jsonResponse({
          schemaVersion: "vitlane.order-sheet.v1",
          orderSheet: sheetFixture({ state: "EDITING" }),
        }, 201);
      }
      if (path.endsWith("/preflight")) {
        const ready = reviewReadySheet({
          state: "READY", version: 2, paymentSelection: "TVITUSD", displayedSnapshotHash: "sha256:displayed",
        });
        ready.merchantCheckouts = ready.merchantCheckouts.map((checkout) => ({
          ...checkout,
          procurementHandling: "OPERATOR_LATER" as const,
          providerNotices: [
            { source: "MESSAGE" as const, type: "error", severity: "requires_buyer_input", code: "extension_interaction_required", presentation: "INTERNAL" as const, audience: "OPERATOR" as const, registered: true },
            { source: "MESSAGE" as const, type: "error", severity: "requires_buyer_input", code: "redirect_to_checkout_required", presentation: "INTERNAL" as const, audience: "OPERATOR" as const, registered: true },
          ],
        }));
        return jsonResponse({ schemaVersion: "vitlane.order-sheet.v1", orderSheet: ready });
      }
      if (path.endsWith("/issue")) {
        return jsonResponse({
          schemaVersion: "vitlane.agency-order.v1",
          agencyOrder: { id: "order-2" },
          paymentInstruction: { id: "instruction-2" },
        }, 201);
      }
      throw new Error(`unexpected request ${path}`);
    }));
    const view = await mount();
    await settle();
    await authorizeProcurement(view);

    await act(async () => button(view, "주문하기").click());
    await settle();

    expect(view.textContent).toContain("금액은 확정되었습니다");
    expect(view.textContent).not.toContain("redirect_to_checkout_required");

    await act(async () => button(view, "$11.11로 주문 확정").click());
    await settle();

    expect(view.textContent).toContain("PAYMENT ROUTE order-2");
  });

  it("세 rail이 보여도 고객이 직접 선택하기 전에는 기본 결제수단이 없다", async () => {
    vi.mocked(getAgencyOrderCapability).mockResolvedValueOnce(capabilityFixture({ sandbox: "READY" }));
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({
      schemaVersion: "vitlane.order-sheet.v1",
      orderSheet: sheetFixture(),
    })));
    const view = await mount();
    await settle();

    const paypalRadio = view.querySelector('[role="radio"][aria-label="PayPal Sandbox"]');
    expect(paypalRadio?.getAttribute("aria-checked")).toBe("false");
    const radios = [...view.querySelectorAll('.agency-order-payment-options [role="radio"]')];
    expect(radios.map((item) => item.getAttribute("aria-label"))).toEqual(["PayPal Live", "PayPal Sandbox", "tVITUSD"]);
  });

  it("paypalSandbox 미활성 배포에서도 tVITUSD를 자동 선택하지 않는다", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({
      schemaVersion: "vitlane.order-sheet.v1",
      orderSheet: sheetFixture(),
    })));
    const view = await mount();
    await settle();

    const tvitusdRadio = view.querySelector('[role="radio"][aria-label="tVITUSD"]');
    expect(tvitusdRadio?.getAttribute("aria-checked")).toBe("false");
  });

  it("전화번호의 공백·하이픈·괄호 입력을 blur 시 읽기 쉬운 +1 형식으로 정리한다", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({
      schemaVersion: "vitlane.order-sheet.v1",
      orderSheet: sheetFixture(),
    })));
    const view = await mount();
    await settle();

    const phone = view.querySelector("#order-shipping-phone") as HTMLInputElement;
    await setControlValue(phone, "(202) 555-0123");
    await act(async () => {
      phone.focus();
      phone.blur();
    });

    expect(phone.value).toBe("+1 202 555 0123");
    expect(view.textContent).toContain("숫자만 입력하거나 공백·하이픈·괄호를 섞어도 됩니다");
  });

  async function mount() {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root?.render(
      <LocaleProvider>
        <MemoryRouter initialEntries={["/curations/curation-1/order-sheet?cartVersion=7"]}>
          <Routes>
            <Route path="/curations/:curationId/order-sheet" element={<OrderSheetPage />} />
            <Route path="/agencyOrder/:agencyOrderId/payment" element={<PaymentRoute />} />
          </Routes>
        </MemoryRouter>
      </LocaleProvider>,
    ));
    return container;
  }
});

function PaymentRoute() {
  const { agencyOrderId } = useParams();
  return <div>PAYMENT ROUTE {agencyOrderId}</div>;
}

function reviewReadySheet(overrides: Partial<OrderSheet> = {}) {
  const sheet = sheetFixture(overrides);
  sheet.merchantCheckouts = sheet.merchantCheckouts.map((checkout) => ({
    ...checkout,
    providerStatus: "requires_escalation",
    quoteReadiness: "CONFIRMED",
    procurementHandling: "OPERATOR_LATER",
    providerNotices: [{ source: "MESSAGE", type: "warning", severity: "requires_buyer_review", code: "extension_interaction_required", presentation: "NOTICE", audience: "CUSTOMER_AND_OPERATOR", registered: true }],
  }));
  return sheet;
}

function sheetFixture(overrides: Partial<OrderSheet> = {}): OrderSheet {
  return {
    id: "sheet-1",
    sourceCart: { cartId: "cart-1", cartVersion: 7, snapshotHash: "sha256:cart" },
    lines: [{
      lineId: "line-1", sourceCartItemId: "item-1", planTargetId: "target-1",
      candidateId: "candidate-1", productTitle: "Domestic item",
      variantTitle: "Default", selectedOptions: [], quantity: 1,
      unitPrice: { amountMinor: 1000, currency: "USD" },
      lineSubtotal: { amountMinor: 1000, currency: "USD" }, shopDomain: "shop.example",
    }],
    shippingAddress: {
      snapshotRef: "shipping-1", snapshotRevision: 1, snapshotHash: "sha256:shipping",
      maskedSummary: "Seattle, WA 98***, US", country: "US",
    },
    shippingAddressInput: {
      recipientName: "Test Buyer", addressLine1: "123 Test Street", addressLine2: "",
      city: "Seattle", region: "WA", postalCode: "98101", country: "US", phone: "+12065550100",
    },
    shippingAddressSource: "ORDER_SHEET",
    merchantCheckouts: [{
      merchantId: "merchant-1", shopDomain: "shop.example", providerStatus: "delivery_options",
      deliveryOptions: [], selectedDeliveryOptionRef: "standard", totals: [],
      authoritativeTotal: { amountMinor: 1100, currency: "USD" },
      taxTotal: { amountMinor: 100, currency: "USD" }, dutiesDisposition: "NO_DUTY_OR_CROSS_BORDER_SIGNAL_AT_PREFLIGHT",
      quoteReadiness: "ESTIMATED",
      procurementHandling: "NORMAL",
      deliveryGroups: [{
        id: "group-1", lineRefs: ["line-1"], selectedOptionRef: "standard",
        options: [{ id: "standard", title: "Standard", amountMinor: 0, currency: "USD" }],
      }],
    }],
    passThroughTotal: { amountMinor: 1100, currency: "USD" },
    agencyFee: { amountMinor: 11, currency: "USD" },
    customerPayableTotal: { amountMinor: 1111, currency: "USD" },
    version: 1,
    state: "EDITING",
    // 유효한(만료되지 않은) 주문서를 표현한다. 과거 시각이면 만료 감지가
    // 즉시 발동해 CTA가 잠긴다.
    expiresAt: new Date(Date.now() + 20 * 60 * 1000).toISOString(),
    ...overrides,
  };
}

const emptyAddress = {
  recipientName: "", addressLine1: "", addressLine2: "", city: "", region: "",
  postalCode: "", country: "US" as const, phone: "",
};

async function fillShipping(root: ParentNode) {
  const values: Record<string, string> = {
    "order-shipping-recipient": "Test Buyer",
    "order-shipping-line-1": "123 Test Street",
    "order-shipping-city": "Seattle",
    "order-shipping-region": "WA",
    "order-shipping-postal": "98101",
    "order-shipping-phone": "+12065550100",
  };
  for (const [id, value] of Object.entries(values)) {
    const input = root.querySelector(`#${id}`) as HTMLInputElement;
    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
  }
}

async function setControlValue(control: HTMLInputElement | HTMLSelectElement, value: string) {
  const prototype = control instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  await act(async () => {
    Object.getOwnPropertyDescriptor(prototype, "value")?.set?.call(control, value);
    control.dispatchEvent(new Event("input", { bubbles: true }));
    control.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

async function authorizeProcurement(root: ParentNode) {
  if (!root.querySelector('.agency-order-payment-options [role="radio"][aria-checked="true"]')) {
    await act(async () => radio(root, "tVITUSD").click());
  }
  const checkboxes = Array.from(
    root.querySelectorAll(".agency-order-procurement-authorization__consent [role=\"checkbox\"]"),
  );
  if (checkboxes.length !== 2) {
    throw new Error(`expected two procurement authorization checkboxes, got ${checkboxes.length}`);
  }
  for (const checkbox of checkboxes) {
    if (!(checkbox instanceof HTMLButtonElement)) {
      throw new Error("procurement authorization checkbox is not a button");
    }
    await act(async () => checkbox.click());
  }
}

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function button(root: ParentNode, name: string) {
  const found = Array.from(root.querySelectorAll("button")).find(
    (candidate) => candidate.textContent?.trim() === name,
  );
  if (!(found instanceof HTMLButtonElement)) throw new Error(`button not found: ${name}`);
  return found;
}

function radio(root: ParentNode, label: string) {
  const found = root.querySelector(`[role="radio"][aria-label="${label}"]`);
  if (!(found instanceof HTMLButtonElement)) throw new Error(`radio not found: ${label}`);
  return found;
}

async function settle() {
  await act(async () => {
    await Promise.resolve();
    await new Promise((resolve) => window.setTimeout(resolve, 0));
  });
}
