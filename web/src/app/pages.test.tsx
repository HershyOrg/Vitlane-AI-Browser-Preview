import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { initialPlanForm } from "../products/curation/planning/domain/form";
import { PlanCreator } from "../products/curation/planning/iface/PlanCreator";

describe("Curation page routing", () => {
  it("Home 본문은 새 Curation 입력만 렌더링하고 진행 목록을 반복하지 않는다", () => {
    const html = renderToStaticMarkup(
      <PlanCreator
        initialForm={initialPlanForm}
        working={false}
        onSubmit={async () => undefined}
      />,
    );

    expect(html.match(/<h1/g)).toHaveLength(1);
    expect(html).not.toContain(">새 구매 요청<");
    expect(html).not.toContain("진행 중인 큐레이션");
    expect(html).toContain('class="shell-intent-composer ');
  });
});
