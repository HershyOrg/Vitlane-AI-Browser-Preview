import { describe, expect, it } from "vitest";
import type { Localize } from "../i18n";
import { textConstraint, textLength } from "./textConstraint";

const l: Localize = (en, ko, params) => {
  let text = ko;
  for (const [key, value] of Object.entries(params ?? {})) text = text.replaceAll(`{${key}}`, String(value));
  void en;
  return text;
};

describe("textConstraint", () => {
  it("counts trimmed characters by code point", () => {
    expect(textLength("  짧음  ")).toBe(2);
    expect(textLength("a😀")).toBe(2);
  });

  it("states the rule with the live count while the value is valid", () => {
    const result = textConstraint({ value: "새 배송 조건 확인", min: 8, max: 2_000, l });
    expect(result.ready).toBe(true);
    expect(result.error).toBeUndefined();
    expect(result.hint).toBe("8~2,000자 · 현재 10자");
  });

  it("flags a value under the minimum as soon as typing starts", () => {
    const result = textConstraint({ value: "짧음", min: 8, max: 2_000, l });
    expect(result.ready).toBe(false);
    expect(result.error).toBe("8자 이상 입력하세요 (6자 더 필요).");
  });

  it("flags a value over the maximum with the overflow", () => {
    const result = textConstraint({ value: "abcdefghijk", min: 1, max: 10, l });
    expect(result.ready).toBe(false);
    expect(result.error).toBe("10자 이내로 입력하세요 (1자 초과).");
  });

  it("keeps an untouched empty required field quiet and flags it after blur", () => {
    expect(textConstraint({ value: "", min: 1, max: 500, l }).error).toBeUndefined();
    expect(textConstraint({ value: "", min: 1, max: 500, touched: true, l }).error).toBe("필수 입력입니다.");
    expect(textConstraint({ value: "", min: 1, max: 500, l }).hint).toBe("1~500자 · 현재 0자");
  });

  it("treats an optional field as ready when empty", () => {
    const result = textConstraint({ value: "", max: 4_000, required: false, l });
    expect(result.ready).toBe(true);
    expect(result.error).toBeUndefined();
    expect(result.hint).toBe("최대 4,000자 · 현재 0자");
  });
});
