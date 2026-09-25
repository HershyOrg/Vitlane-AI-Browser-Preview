import { expect, it } from "vitest";
import { combinationReplyText, type Combination } from "./combination";
import type { ThreadResponse } from "./thread";

const response = { body: "[[c1]]과 [[c2]]를 추천해요.", references: [] } as unknown as ThreadResponse;
it("keeps ordinary replies unchanged", () => {
 expect(combinationReplyText(response)).toBe(response.body);
});
it("reads combination advice as ordinary paragraphs without fixed headings or amount cards", () => {
 const combination = { reasons: ["차분한 색이 서로 어울려요."], tips: [{ label: "깔끔하게", body: "셔츠를 바지에 넣어 입어보세요." }], cautions: ["신발은 포함하지 않았어요."], budgetAdvice: "남는 예산은 쓰지 않아도 돼요." } as Combination;
 const text = combinationReplyText({ ...response, combination });
 expect(text).toBe("[[c1]]과 [[c2]]를 추천해요.\n\n차분한 색이 서로 어울려요. 셔츠를 바지에 넣어 입어보세요.\n\n신발은 포함하지 않았어요.\n\n남는 예산은 쓰지 않아도 돼요.");
 expect(text).not.toContain("조합 상품 합계");
 expect(text).not.toContain("깔끔하게");
});
