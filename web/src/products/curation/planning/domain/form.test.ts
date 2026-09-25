import { describe, expect, it } from "vitest";
import { listFromText, optionalMoney } from "./form";

describe("planning form normalization", () => {
  it("쉼표 입력을 빈 값 없는 목록으로 만든다", () => {
    expect(listFromText("경량, 접이식, , 방수")).toEqual([
      "경량",
      "접이식",
      "방수",
    ]);
  });

  it("가격이 비어 있으면 범위를 제거한다", () => {
    expect(optionalMoney(" ", "USD")).toBeNull();
    expect(optionalMoney("30.50", "USD")).toEqual({
      amount: "30.50",
      currency: "USD",
    });
  });
});
