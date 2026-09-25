import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { UnitStage } from "./agencyOrderApi";
import { unitStageLabel } from "../iface/unitStageCopy";

// BE 골든 fixture(shared/openapi/fixtures — Go 테스트가 생성·검증)를 그대로
// 파싱해 unit stage 어휘와 FE 라벨 사전의 동기를 지킨다(ADR-0057). BE가 새
// stage를 추가하면 라벨 사전이 갱신되기 전에는 이 테스트가 깨진다.

type Fixture = {
  schemaVersion: string;
  stages: UnitStage[];
  cases: Array<{ name: string; refundStatus: string; stage: UnitStage }>;
};

const fixturePath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../../../../shared/openapi/fixtures/agency-order-units.v2.json",
);
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as Fixture;

describe("agency-order units contract", () => {
  it("BE stage 어휘 전부에 FE 라벨이 있다", () => {
    expect(fixture.schemaVersion).toBe("vitlane.agency-order-units.v2");
    expect(fixture.stages.length).toBeGreaterThan(0);
    for (const stage of fixture.stages) {
      expect(unitStageLabel(stage), `label for ${stage}`).toBeTruthy();
    }
  });

  it("fixture case의 stage는 선언된 어휘 안에 있다", () => {
    expect(fixture.cases.length).toBeGreaterThan(0);
    for (const testCase of fixture.cases) {
      expect(fixture.stages).toContain(testCase.stage);
    }
  });
});
