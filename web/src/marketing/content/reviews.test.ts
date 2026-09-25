import { describe, expect, it } from "vitest";
import { publishableVoices, type EarlyVoice } from "./reviews";

const voices: readonly EarlyVoice[] = [
  { id: "stub", quote: "stub quote", attribution: "stub", stub: true },
  { id: "real", quote: "real quote", attribution: "real", stub: false },
];

describe("먼저 써 본 사람들 release gate (N8)", () => {
  it("명시적 개발 flag가 없으면 stub 인용을 렌더링하지 않는다", () => {
    expect(publishableVoices(voices).map((voice) => voice.id)).toEqual(["real"]);
  });

  it("stub만 있으면 기본 빌드에서 섹션이 비어 숨겨진다", () => {
    expect(publishableVoices([voices[0]])).toEqual([]);
  });

  it("명시적으로 허용한 개발 빌드만 stub 인용을 보여 준다", () => {
    expect(publishableVoices(voices, true)).toHaveLength(2);
  });
});
