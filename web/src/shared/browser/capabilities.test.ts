import { describe, expect, it } from "vitest";
import {
  browserWalletCapability,
  isLikelyMobileDevice,
} from "./capabilities";

describe("browser capabilities", () => {
  it("일반 모바일과 desktop-mode iPad를 모바일로 판정한다", () => {
    expect(isLikelyMobileDevice("Mozilla/5.0 (iPhone) Mobile", 5)).toBe(true);
    expect(
      isLikelyMobileDevice(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15)",
        5,
      ),
    ).toBe(true);
    expect(isLikelyMobileDevice("Mozilla/5.0 (Windows NT 10.0)", 0)).toBe(false);
  });

  it("지갑 provider와 기기 조건을 구분한다", () => {
    expect(
      browserWalletCapability("Mozilla/5.0 (Windows NT 10.0)", true, 0),
    ).toEqual({ kind: "INJECTED_READY", mobile: false });
    expect(
      browserWalletCapability("Mozilla/5.0 (Windows NT 10.0)", false, 0),
    ).toEqual({ kind: "NO_PROVIDER", mobile: false });
    expect(
      browserWalletCapability("Mozilla/5.0 (Android) Mobile", false, 5),
    ).toEqual({ kind: "UNSUPPORTED_DEVICE", mobile: true });
  });
});
