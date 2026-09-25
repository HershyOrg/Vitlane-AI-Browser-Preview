import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Chip } from "./Chip";

describe("Chip (Still Water 3-1)", () => {
  it("상태는 data-tone 점으로만 표시하고 글자는 문장체 그대로다", () => {
    const markup = renderToStaticMarkup(<Chip tone="progress">판매처 주문 · 주문 중</Chip>);
    expect(markup).toContain('data-tone="progress"');
    expect(markup).toContain("판매처 주문 · 주문 중");
    expect(markup).not.toContain("vt-chip--mode");
  });

  it("TEST/LIVE 경계는 mode 변형, 확인 필요는 attention 변형이다", () => {
    expect(renderToStaticMarkup(<Chip mode="test">테스트 결제</Chip>)).toContain("vt-chip--mode is-test");
    expect(renderToStaticMarkup(<Chip mode="live">Live 결제</Chip>)).toContain("vt-chip--mode is-live");
    expect(renderToStaticMarkup(<Chip attention>확인 필요</Chip>)).toContain("vt-chip--attention");
  });

  it("라벨 chip은 점도 tone 변형도 없다", () => {
    const markup = renderToStaticMarkup(<Chip>담당 가능</Chip>);
    expect(markup).not.toContain("data-tone");
    expect(markup).toContain('class="vt-chip"');
  });
});
