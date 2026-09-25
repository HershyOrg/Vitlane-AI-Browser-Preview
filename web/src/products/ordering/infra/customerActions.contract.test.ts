import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { AgencyOrderProjection, CustomerAction, CustomerActionKind } from "./agencyOrderApi";
import { customerActionOf } from "./agencyOrderApi";

// BE 골든 fixture(shared/openapi/fixtures — Go 테스트가 생성·검증)를 그대로
// 파싱해 FE 계약과 대조한다(ADR-0055 §5). BE가 새 액션 kind를 추가하면 FE
// 렌더 공간(KNOWN_KINDS)이 함께 갱신되기 전에는 이 테스트가 깨진다.

const KNOWN_KINDS: CustomerActionKind[] = [
  "PAY", "CANCEL_PRE_EFFECT", "CANCEL_DELAY_RULE", "REQUEST_REFUND",
];

type Fixture = {
  schemaVersion: string;
  cases: Array<{
    name: string;
    now: string;
    projection: AgencyOrderProjection;
    availableActions: CustomerAction[];
  }>;
};

const fixturePath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
	"../../../../../shared/openapi/fixtures/agency-order-customer-actions.v2.json",
);
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as Fixture;

describe("agency-order customer actions contract", () => {
  it("fixture는 FE 타입으로 파싱되고 액션 kind는 렌더 공간의 닫힌 집합이다", () => {
		expect(fixture.schemaVersion).toBe("vitlane.agency-order-customer-actions.v2");
    expect(fixture.cases.length).toBeGreaterThan(0);
    for (const testCase of fixture.cases) {
      expect(testCase.projection.availableActions).toEqual(testCase.availableActions);
      for (const action of testCase.availableActions) {
        expect(KNOWN_KINDS).toContain(action.kind);
      }
    }
  });

	it("REQUEST_REFUND 액션의 대상 MO·사유 어휘가 FE 입력 공간과 맞는다", () => {
    const refundCases = fixture.cases.filter((testCase) =>
      customerActionOf(testCase.projection, "REQUEST_REFUND"));
    expect(refundCases.length).toBeGreaterThan(0);
    for (const testCase of refundCases) {
      const action = customerActionOf(testCase.projection, "REQUEST_REFUND");
			const merchantOrderIds = new Set(testCase.projection.merchantOrders.map((order) => order.id));
			for (const id of action?.eligibleMerchantOrderIds ?? []) {
				expect(merchantOrderIds.has(id)).toBe(true);
      }
      expect(action?.reasonCodes ?? []).not.toContain("CHANGE_OF_MIND");
      expect((action?.reasonCodes ?? []).length).toBeGreaterThan(0);
    }
  });

  it("PAY 액션은 rail 축을 동반한다 — 결제 화면 라우팅의 유일한 근거다", () => {
    const payCases = fixture.cases.filter((testCase) =>
      customerActionOf(testCase.projection, "PAY"));
    expect(payCases.length).toBeGreaterThan(0);
    for (const testCase of payCases) {
      expect(["GIWA", "PAYPAL"]).toContain(
        customerActionOf(testCase.projection, "PAY")?.rail,
      );
    }
  });
});
