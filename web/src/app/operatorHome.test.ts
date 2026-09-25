import { describe, expect, it } from "vitest";
import { operatorHomePath } from "./operatorHome";

describe("operatorHomePath", () => {
  it("운영자의 첫 화면은 주문 처리, 순수 marketing admin은 운영 현황이다", () => {
    expect(operatorHomePath({
      marketingAdmin: true,
      phase5Operator: true,
    })).toBe("/admin/agencyOrder");
    expect(operatorHomePath({ phase5Operator: true })).toBe("/admin/agencyOrder");
    expect(operatorHomePath({ marketingAdmin: true })).toBe("/admin/ops");
  });

  it("운영 권한이 없으면 구매 홈으로 돌아간다", () => {
    expect(operatorHomePath({})).toBe("/");
  });
});
