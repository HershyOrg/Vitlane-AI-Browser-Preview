// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Candidate } from "../../../../shared/api/types";
import {
  candidatePath,
  candidateVariantFields,
  CandidateConfigurationEditor,
} from "./CandidateConfigurationEditor";

const candidate: Candidate = {
  id: "candidate-variant",
  researchSubmissionId: "submission-1",
  shoppingSessionId: "session-1",
  productUrl: "https://store.example/products/jacket",
  merchantDomain: "store.example",
  category: "jacket",
  name: "Field Jacket",
  description: "테스트 재킷",
  price: { amount: "89.00", currency: "USD" },
  variantDiscovery: {
    status: "OBSERVED_PARTIAL",
    observedAt: "2026-07-25T00:00:00Z",
    evidence: { summary: "상품 페이지의 option selector를 관찰함" },
    fields: [
      {
        key: "color",
        label: "Color",
        inputKind: "ENUM",
        required: true,
        knownValues: [
          { value: "black", label: "Black" },
          { value: "white", label: "White" },
        ],
        source: "AGENT_OBSERVATION",
        discoveryStatus: "OBSERVED_PARTIAL",
      },
      {
        key: "size",
        label: "Size",
        inputKind: "ENUM_OR_VALUE",
        required: true,
        knownValues: [
          { value: "m", label: "M" },
          { value: "l", label: "L" },
        ],
        source: "AGENT_OBSERVATION",
        discoveryStatus: "OBSERVED_PARTIAL",
      },
    ],
  },
  orderability: {
    providerKind: "GENERIC_WEB",
    executionMode: "MANUAL_MERCHANT_ORDER",
    externalEffect: "SIMULATED",
    liveOrderability: "UNVERIFIED",
    settlementStatus: "SUPPORTED",
    status: "TEST_ORDER_FLOW_AVAILABLE",
  },
  evidence: {
    summary: "예산 내 상품",
    matchedCriteria: ["재킷"],
    tradeoffs: [],
    sourceUrls: ["https://store.example/products/jacket"],
  },
  observedAt: "2026-07-25T00:00:00Z",
  orderSupport: "UNKNOWN",
  eligibility: {
    hardChecks: "PASS",
    semanticReview: "REQUIRED",
    reasonCodes: [],
    policyVersion: "vitlane.research-policy.v1",
  },
  candidateHashSchema: "vitlane.candidate.v3",
  candidateHash: "candidate-hash",
  orderIndex: 0,
  createdAt: "2026-07-25T00:00:00Z",
};

describe("CandidateConfigurationEditor", () => {
  let container: HTMLDivElement | undefined;

  afterEach(() => {
    container?.remove();
    container = undefined;
    vi.clearAllMocks();
  });

  it("Agent 관찰 field를 초기값으로 사용하고 required 선택과 명시 확인 뒤 snapshot을 제출한다", async () => {
    const onConfirm = vi.fn(async () => undefined);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <CandidateConfigurationEditor
          candidate={candidate}
          working={false}
          onCancel={vi.fn()}
          onConfirm={onConfirm}
        />,
      );
    });

    expect(container.textContent).toContain("Agent 관찰");
    expect(container.textContent).toContain("사용자 확정");
    expect(container.textContent).toContain("운영자 검증");
    expect(container.textContent).toContain("Agent 관찰 field");
    expect(container.textContent).toContain("상품 페이지의 option selector를 관찰함");

    const submit = container.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    expect(submit.disabled).toBe(true);

    await setControl(
      container.querySelector<HTMLSelectElement>('[aria-label="Color 선택"]')!,
      "black",
      "change",
    );
    await setControl(
      container.querySelector<HTMLInputElement>('[aria-label="Size 선택"]')!,
      "xl",
      "input",
    );
    const confirmations = container.querySelectorAll<HTMLButtonElement>(
      '[data-slot="checkbox"]',
    );
    await act(async () => confirmations[confirmations.length - 1].click());
    expect(submit.disabled).toBe(false);

    await act(async () => {
      submit.click();
      await Promise.resolve();
    });

    expect(onConfirm).toHaveBeenCalledWith({
      fields: candidate.variantDiscovery!.fields,
      selections: { color: "black", size: "xl" },
      confirmsNoOptions: false,
    });
    await act(async () => root.unmount());
  });

  it("누락 field를 추가·수정·제거하고 옵션 없음을 별도로 명시할 수 있다", async () => {
    const onConfirm = vi.fn(async () => undefined);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => {
      root.render(
        <CandidateConfigurationEditor
          candidate={{ ...candidate, variantDiscovery: { ...candidate.variantDiscovery!, fields: [] } }}
          working={false}
          onCancel={vi.fn()}
          onConfirm={onConfirm}
        />,
      );
    });

    const add = [...container.querySelectorAll("button")]
      .find((button) => button.textContent?.includes("옵션 추가"))!;
    await act(async () => add.click());
    expect(container.textContent).toContain("사용자 field");
    expect(container.querySelector('[aria-label="옵션 1 표시 이름"]')).not.toBeNull();

    const remove = [...container.querySelectorAll("button")]
      .find((button) => button.textContent === "옵션 제거")!;
    await act(async () => remove.click());
    expect(container.querySelector('[aria-label="옵션 1 표시 이름"]')).toBeNull();

    const confirmations = container.querySelectorAll<HTMLButtonElement>(
      '[data-slot="checkbox"]',
    );
    await act(async () => confirmations[0].click());
    const currentConfirmations = container.querySelectorAll<HTMLButtonElement>(
      '[data-slot="checkbox"]',
    );
    await act(async () => currentConfirmations[currentConfirmations.length - 1].click());
    const submit = container.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    expect(submit.disabled).toBe(false);
    await act(async () => {
      submit.click();
      await Promise.resolve();
    });
    expect(onConfirm).toHaveBeenCalledWith({
      fields: [],
      selections: {},
      confirmsNoOptions: true,
    });
    await act(async () => root.unmount());
  });

  it("새 VariantDiscovery와 명시된 non-USD 구매 경로만 해석한다", () => {
    const nonUSD = {
      ...candidate,
      price: { amount: "10000", currency: "KRW" },
      orderability: {
        ...candidate.orderability,
        settlementStatus: "UNSUPPORTED" as const,
        status: "SETTLEMENT_CURRENCY_UNSUPPORTED" as const,
      },
    };
    expect(candidateVariantFields(nonUSD)).toEqual(
      candidate.variantDiscovery.fields,
    );
    expect(candidatePath(nonUSD).status).toBe(
      "SETTLEMENT_CURRENCY_UNSUPPORTED",
    );
  });
});

async function setControl(
  control: HTMLInputElement | HTMLSelectElement,
  value: string,
  eventName: "input" | "change",
) {
  await act(async () => {
    const valueSetter = Object.getOwnPropertyDescriptor(
      Object.getPrototypeOf(control),
      "value",
    )?.set;
    valueSetter?.call(control, value);
    control.dispatchEvent(new Event(eventName, { bubbles: true }));
  });
}
