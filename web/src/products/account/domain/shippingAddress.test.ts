import { describe, expect, it } from "vitest";
import {
  formatUSPhoneInput,
  shippingAddressErrorSummary,
  validateShippingAddress,
} from "./shippingAddress";

const validAddress = {
  label: "기본 배송지",
  recipientName: "Test Buyer",
  addressLine1: "123 Test Street",
  addressLine2: "",
  city: "Seattle",
  region: "WA",
  postalCode: "98101",
  country: "US",
  phone: "+12065550100",
};

describe("shipping address validation", () => {
  it("서버와 같은 필수 항목 및 우편번호 오류를 필드별로 설명한다", () => {
    const errors = validateShippingAddress({
      ...validAddress,
      recipientName: "",
      region: "",
      postalCode: "?",
    }, { requireLabel: true });

    expect(errors).toEqual({
      recipientName: "수령인을 입력해 주세요.",
      region: "주(State)를 입력해 주세요.",
      postalCode: "우편번호는 문자·숫자·공백·하이픈 2~16자로 입력해 주세요.",
    });
    expect(shippingAddressErrorSummary(errors)).toBe("확인이 필요한 배송지 항목이 3개 있습니다.");
  });

  it("OrderSheet에서는 미국 외 배송지를 차단한다", () => {
    expect(validateShippingAddress(
      { ...validAddress, country: "CA" },
      { requireUS: true },
    ).country).toBe("현재 주문서는 미국(US) 배송지만 지원합니다.");
    expect(validateShippingAddress(validAddress, { requireUS: true })).toEqual({});
  });

  it("OrderSheet에서는 provider가 거절하는 미국 형식을 제출 전에 필드별로 알린다", () => {
    const errors = validateShippingAddress({
      ...validAddress,
      region: "Washington",
      postalCode: "M5V 3A8",
      phone: "01012345678",
    }, { requireUS: true });
    expect(errors.region).toContain("미국 주 코드");
    expect(errors.postalCode).toContain("ZIP");
    expect(errors.phone).toContain("+1");
  });

  it("OrderSheet 전화는 구두점을 허용하고 +1 미국 형식만 통과시킨다", () => {
    expect(validateShippingAddress(
      { ...validAddress, phone: "(202) 555-0123" },
      { requireUS: true },
    )).toEqual({});
    expect(validateShippingAddress(
      { ...validAddress, phone: "+82 10-1234-5678" },
      { requireUS: true },
    ).phone).toContain("+1");
    expect(validateShippingAddress(
      { ...validAddress, phone: "" },
      { requireUS: true },
    ).phone).toContain("+1");
  });

  it.each([
    ["2025550123", "+1 202 555 0123"],
    ["202-555-0123", "+1 202 555 0123"],
    ["(202) 555-0123", "+1 202 555 0123"],
    ["1 202.555.0123", "+1 202 555 0123"],
    ["+1-202-555-0123", "+1 202 555 0123"],
  ])("OrderSheet 전화 입력 %s을 읽기 쉬운 +1 형식으로 정리한다", (input, expected) => {
    expect(formatUSPhoneInput(input)).toBe(expected);
  });

  it("불완전하거나 미국 형식이 아닌 전화는 사용자가 고칠 수 있게 원문을 보존한다", () => {
    expect(formatUSPhoneInput("202-55")).toBe("202-55");
    expect(formatUSPhoneInput("+82 10-1234-5678")).toBe("+82 10-1234-5678");
  });

  it("Account 프로필 저장은 미국 외 주소와 자유 형식 전화를 계속 허용한다", () => {
    expect(validateShippingAddress({
      ...validAddress,
      country: "KR",
      region: "서울",
      postalCode: "04524",
      phone: "01012345678",
    }, { requireLabel: true })).toEqual({});
  });
});
