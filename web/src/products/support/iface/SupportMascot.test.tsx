import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SupportMascot } from "./SupportMascot";

describe("SupportMascot", () => {
  it("인스턴스마다 clip-path id가 달라 한 화면에 여러 개가 있어도 id가 겹치지 않는다", () => {
    const markup = renderToStaticMarkup(
      <>
        <SupportMascot className="support-chat__avatar" />
        <SupportMascot className="support-chat__bubble-avatar" />
      </>,
    );
    const ids = [...markup.matchAll(/<clipPath id="([^"]+)"/g)].map((match) => match[1]);
    expect(ids).toHaveLength(2);
    expect(new Set(ids).size).toBe(2);
    for (const id of ids) {
      expect(id).toMatch(/^support-mascot-[A-Za-z0-9_-]+$/);
      expect(markup).toContain(`clip-path="url(#${id})"`);
    }
  });

  it("입 path 없이 몸통·V 띠·눈만 있고 색은 토큰만 쓴다", () => {
    const markup = renderToStaticMarkup(<SupportMascot />);
    expect(markup).not.toContain("M52 83");
    expect((markup.match(/<ellipse /g) ?? []).length).toBe(2);
    expect(markup).toContain("var(--vt-semantic-color-action-brand)");
    expect(markup).toContain("var(--vt-semantic-color-text-accent)");
    expect(markup).toContain("var(--vt-semantic-color-surface-base)");
    expect(markup).not.toMatch(/#[0-9a-fA-F]{3,6}\b/);
  });
});
