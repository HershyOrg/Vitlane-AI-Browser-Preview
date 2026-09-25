// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { APIError } from "../../../../shared/api/client";
import type { PlanResult } from "../../../../shared/api/types";
import { curationCreated, curationCreationUnconfirmed } from "../../app/useSidebarCurations";
import { initialPlanForm } from "../domain/form";
import { createPlan } from "../infra/planningApi";
import { usePlanFlow } from "./usePlanFlow";

vi.mock("../infra/planningApi", () => ({ createPlan: vi.fn() }));
const create = vi.mocked(createPlan);

describe("usePlanFlow가 사이드바에 알리는 생성 결과", () => {
  let root: Root;
  let flow: ReturnType<typeof usePlanFlow>;
  const told: string[] = [];
  const listen = (event: Event) => told.push(event.type);

  function Probe() {
    flow = usePlanFlow();
    return null;
  }

  async function submit() {
    told.length = 0;
    await act(async () => {
      await flow.submitPlan(initialPlanForm);
    });
  }

  beforeEach(async () => {
    create.mockReset();
    window.addEventListener(curationCreated, listen);
    window.addEventListener(curationCreationUnconfirmed, listen);
    const element = document.createElement("div");
    document.body.append(element);
    root = createRoot(element);
    await act(async () => root.render(<Probe />));
  });
  afterEach(async () => {
    window.removeEventListener(curationCreated, listen);
    window.removeEventListener(curationCreationUnconfirmed, listen);
    await act(async () => root.unmount());
    document.body.innerHTML = "";
  });

  it("성공하면 응답의 행을 알린다", async () => {
    create.mockResolvedValueOnce({
      plan: { originalIntent: "가벼운 러닝화" },
      curation: { id: "c1", createdAt: "2026-09-16T09:00:00Z" },
    } as unknown as PlanResult);
    await submit();
    expect(told).toEqual([curationCreated]);
  });

  it("결과를 모르는 실패 뒤에는 새 것 확인을 알린다", async () => {
    for (const failure of [
      new TypeError("Failed to fetch"),
      new SyntaxError("Unexpected token '<'"),
      new APIError("INTERNAL_ERROR", "요청을 처리하지 못했습니다.", 502),
    ]) {
      create.mockRejectedValueOnce(failure);
      await submit();
      expect(told, String(failure)).toEqual([curationCreationUnconfirmed]);
      expect(flow.error).toBeTruthy();
    }
  });

  it("서버가 거절하면 저장된 것이 없으니 알리지 않는다", async () => {
    create.mockRejectedValueOnce(new APIError("VALIDATION_ERROR", "예산을 확인해 주세요.", 400));
    await submit();
    expect(told).toEqual([]);
    expect(flow.error).toBe("예산을 확인해 주세요.");
  });
});
