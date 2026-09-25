import { describe, expect, it } from "vitest";
import { effectText } from "./CurationThread";
import type { Localize } from "../../../shared/i18n";
import type { ResearchCriteria } from "../domain/researchCriteria";

describe("thread criteria receipts", () => {
 it.each(["ko-KR","en-US"])("shows the committed changes and original target/axis labels (%s)", locale => {
  const l:Localize=(en,ko,values)=>Object.entries(values??{}).reduce((s,[k,v])=>s.replaceAll(`{${k}}`,String(v)),locale==="ko-KR"?ko:en);
  const before:ResearchCriteria={schemaVersion:"vitlane.target-criteria.v1",version:1,subject:{label:"Keyboard",productType:"keyboard"},axes:[{axisId:"comfort",label:"Comfort",definition:"Comfort",importance:2,usesPrice:false,usesVisualEvidence:false,origin:"REQUEST"}],exclusions:["used"]};
  const after:ResearchCriteria={...before,version:2,axes:[{...before.axes[0],importance:4},{...before.axes[0],axisId:"quiet",label:"Quiet",definition:"Quiet",origin:"FEEDBACK"}],exclusions:["wired"]};
  const text=effectText({kind:"CRITERIA_CHANGED",targetId:"keyboard",before,after},l,locale,{keyboard:"Work keyboard"});
  expect(text).toContain("Work keyboard");expect(text).toContain("Comfort");expect(text).toContain("2 → 4");expect(text).toContain("Quiet");expect(text).toContain("wired");expect(text).toContain("used");
  expect(text).toContain(locale==="ko-KR"?"추가":"Added");expect(text).toContain(locale==="ko-KR"?"제외 해제":"No longer exclude");
 });
});
