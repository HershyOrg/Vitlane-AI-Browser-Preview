// 현재 SINGLE/AUTO 입력 화면의 사용자 언어 contract를 검증한다.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { initialPlanForm } from "../domain/form";
import { PlanCreator } from "./PlanCreator";

describe("Planning views", () => {
  it("구매 항목 구성 방식과 요청 action을 사용자 언어로 렌더링한다", () => {
    const html = renderToStaticMarkup(
      <PlanCreator
        initialForm={initialPlanForm}
        working={false}
        onSubmit={async () => undefined}
      />,
    );
    expect(html).toContain("찾고 있는 상품을 입력해주세요");
    expect(html).toContain("예산 설정");
    expect(html).not.toContain("단일 상품");
    expect(html).toContain("상품 찾기 시작");
    expect(html).toContain("조사·보기 설정");
    expect(html).not.toContain("설정하기");
    expect(html).not.toContain("→ 버튼은 Intent");
    expect(html).not.toContain("shell-intent-assurance");
    expect(html).not.toContain("Target");
    expect(html).not.toContain("계획 확정");
  });
});
