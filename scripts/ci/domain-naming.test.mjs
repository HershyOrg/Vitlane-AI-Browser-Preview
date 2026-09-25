import test from "node:test";
import assert from "node:assert/strict";
import { namingViolations } from "./domain-naming.mjs";

test("new internal numbered names are rejected", () => {
  assert.ok(namingViolations("server/model.go", "type Phase12Cart struct {}").length);
  assert.ok(namingViolations("web/model.ts", "const loadPhase8Cart = () => 1;").length);
  assert.ok(namingViolations("web/phase6.css", ".shell { color: inherit; }").length);
  assert.ok(namingViolations("web/model.css", ".phase9-panel { color: inherit; }").length);
});

test("deployed storage and environment strings are preserved", () => {
  const source = [
    'const query = "SELECT * FROM phase8_research_candidates";',
    'const flag = "PHASE5_SETTLEMENT_ENABLED";',
    'const operator = response.phase5Operator;',
    '// Phase 5 deployment history.',
    'type CatalogCart = { version: number };',
  ].join("\n");
  assert.deepEqual(namingViolations("web/model.ts", source), []);
});

test("the existing wire-field exception cannot cover a new numbered symbol", () => {
  assert.ok(namingViolations("web/model.ts", "const phase5OperatorCache = {};").length);
  assert.ok(namingViolations("web/model.ts", "response.phase6Operator;").length);
});

test("strings and comments do not hide subsequent code", () => {
  assert.ok(namingViolations("server/model.go",
    'const label = "phase8\\" label"\n// historical\nvar phase8Worker int').length);
  assert.deepEqual(namingViolations("web/model.css",
    "/* historical Phase8 */ .catalog-ui-panel { color: inherit; }"), []);
});

test("JS regex and nested templates are parsed without false identifiers", () => {
  const bt = String.fromCharCode(96);
  assert.deepEqual(namingViolations("test.cjs",
    'const pattern = /phase4[\\\\/]/; const message = ' + bt +
    'phase8 $' + '{"Phase8"}' + bt + ';'), []);
  assert.ok(namingViolations("test.cjs",
    'const message = ' + bt + '$' + '{loadPhase8Cart()}' + bt + ';').length);
});
