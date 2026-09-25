import { describe, expect, it, vi } from "vitest";
import { randomUUID } from "./randomUUID";

describe("randomUUID", () => {
  it("브라우저의 native randomUUID를 우선 사용한다", () => {
    const native = vi.fn(() => "11111111-1111-4111-8111-111111111111");
    const getRandomValues = vi.fn();

    expect(
      randomUUID({
        randomUUID: native,
        getRandomValues,
      } as unknown as Crypto),
    ).toBe("11111111-1111-4111-8111-111111111111");
    expect(native).toHaveBeenCalledOnce();
    expect(getRandomValues).not.toHaveBeenCalled();
  });

  it("비보안 preview origin에서는 getRandomValues로 RFC 4122 v4 ID를 만든다", () => {
    const getRandomValues = vi.fn((values: Uint8Array) => {
      values.set([
        0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x06, 0x77, 0xc8, 0x99, 0xaa,
        0xbb, 0xcc, 0xdd, 0xee, 0xff,
      ]);
      return values;
    });

    expect(
      randomUUID({
        getRandomValues,
      } as unknown as Crypto),
    ).toBe("00112233-4455-4677-8899-aabbccddeeff");
    expect(getRandomValues).toHaveBeenCalledOnce();
  });
});
