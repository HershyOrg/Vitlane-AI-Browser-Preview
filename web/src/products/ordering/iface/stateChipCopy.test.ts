import { describe, expect, it } from "vitest";
import {
  fundingStateLabel,
  humanizeState,
  merchantOrderStateLabel,
  paymentStateLabel,
  stateTone,
} from "./stateChipCopy";

const ko = (_english: string, korean: string) => korean;
const en = (english: string) => english;

describe("상태 칩 문구 (ADR-0073 PR E)", () => {
  it("알려진 상태는 문장체 한국어/영어 쌍으로 나온다", () => {
    expect(merchantOrderStateLabel("PLANNED", ko)).toBe("예정");
    expect(merchantOrderStateLabel("PLANNED", en)).toBe("Planned");
    expect(fundingStateLabel("AVAILABLE", ko)).toBe("확보됨");
    expect(paymentStateLabel("FINALIZED", ko)).toBe("확정됨");
  });

  it("모르는 서버 상태도 raw SNAKE_CASE로 노출하지 않는다", () => {
    expect(humanizeState("SOME_NEW_STATE")).toBe("Some new state");
    expect(merchantOrderStateLabel("SOME_NEW_STATE", ko)).toBe("Some new state");
  });

  it("점의 색은 진행·완료·대기·실패 네 가지뿐이다", () => {
    expect(stateTone("PLACEMENT_PENDING")).toBe("progress");
    expect(stateTone("PLACED")).toBe("done");
    expect(stateTone("FAILED")).toBe("failed");
    expect(stateTone(undefined)).toBe("waiting");
  });
});
