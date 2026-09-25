import { describe, expect, it } from "vitest";
import type { SupportMessage } from "./message";
import { mergeMessages, unreadReplyCount } from "./thread";

const message = (
  id: string,
  createdAt: string,
  overrides: Partial<SupportMessage> = {},
): SupportMessage => ({
  id,
  author: "CUSTOMER",
  body: `본문 ${id}`,
  createdAt,
  ...overrides,
});

describe("mergeMessages", () => {
  it("id로 중복을 접고 시간 오름차순으로 정렬한다", () => {
    const existing = [
      message("m2", "2026-08-23T09:05:00Z"),
      message("m1", "2026-08-23T09:00:00Z"),
    ];
    const incoming = [
      message("m3", "2026-08-23T09:10:00Z", { author: "OPERATOR" }),
      message("m2", "2026-08-23T09:05:00Z"),
    ];
    const merged = mergeMessages(existing, incoming);
    expect(merged.map((item) => item.id)).toEqual(["m1", "m2", "m3"]);
  });

  it("같은 id는 서버 응답 상태로 덮는다(읽음 워터마크 반영)", () => {
    const existing = [message("m1", "2026-08-23T09:00:00Z", { author: "OPERATOR" })];
    const incoming = [
      message("m1", "2026-08-23T09:00:00Z", {
        author: "OPERATOR",
        readAt: "2026-08-23T09:30:00Z",
      }),
    ];
    expect(mergeMessages(existing, incoming)[0].readAt).toBe("2026-08-23T09:30:00Z");
  });
});

describe("unreadReplyCount", () => {
  it("읽지 않은 답변만 센다 — 고객 발신은 제외", () => {
    const messages = [
      message("m1", "2026-08-23T09:00:00Z"),
      message("m2", "2026-08-23T09:05:00Z", { author: "OPERATOR" }),
      message("m3", "2026-08-23T09:10:00Z", {
        author: "OPERATOR",
        readAt: "2026-08-23T09:20:00Z",
      }),
    ];
    expect(unreadReplyCount(messages)).toBe(1);
  });
});
