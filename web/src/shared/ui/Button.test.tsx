import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Button, ButtonLink } from "./Button";

describe("Button", () => {
  it("명시하지 않은 button을 submit이나 primary로 만들지 않는다", () => {
    const markup = renderToStaticMarkup(<Button>조건 보기</Button>);

    expect(markup).toContain('type="button"');
    expect(markup).toContain("vt-button--secondary");
    expect(markup).not.toContain("vt-button--primary");
  });

  it("허용된 emphasis와 compact size만 class contract로 표현한다", () => {
    const markup = renderToStaticMarkup(
      <Button emphasis="primary" size="compact">
        구매 계획 확정
      </Button>,
    );

    expect(markup).toContain("vt-button--primary");
    expect(markup).toContain("vt-button--compact");
  });

  it("busy 상태는 중복 실행을 막고 접근성 상태를 노출한다", () => {
    const markup = renderToStaticMarkup(
      <Button busy>구매 계획 저장 중…</Button>,
    );

    expect(markup).toContain('aria-busy="true"');
    expect(markup).toContain("disabled");
  });

  it("ButtonLink가 navigation 의미를 유지하며 같은 강조 계약을 사용한다", () => {
    const markup = renderToStaticMarkup(
      <ButtonLink href="/login" emphasis="primary">
        Google로 계속
      </ButtonLink>,
    );

    expect(markup).toContain('href="/login"');
    expect(markup).toContain("vt-button--primary");
    expect(markup).not.toContain("<button");
  });
});
