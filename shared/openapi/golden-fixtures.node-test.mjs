import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const openapiDirectory = path.dirname(fileURLToPath(import.meta.url));

async function fixture(name) {
  return JSON.parse(
    await readFile(
      path.join(openapiDirectory, "fixtures", name),
      "utf8",
    ),
  );
}

function canonicalize(value) {
  if (
    value === null ||
    typeof value === "boolean" ||
    typeof value === "number" ||
    typeof value === "string"
  ) {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalize).join(",")}]`;
  }
  assert.equal(typeof value, "object");
  return `{${Object.keys(value)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${canonicalize(value[key])}`)
    .join(",")}}`;
}

function sha256Wire(value) {
  return `sha256:${createHash("sha256").update(value, "utf8").digest("hex")}`;
}

test("CurationSelectionSnapshotHash.v1 vector preserves exact snapshot lineage", async () => {
  const vector = await fixture(
    "curation-selection-snapshot-hash.v1.json",
  );
  const canonical = canonicalize(vector.value);
  assert.equal(canonical, vector.canonicalPreimage);
  assert.equal(sha256Wire(canonical), vector.snapshotHash);
});

test("CurationAction command parser vectors cover the closed catalog", async () => {
  const vectors = await fixture(
    "curation-action-command-parser.v1.json",
  );
  assert.equal(vectors.catalog.length, 7);
  assert.equal(
    new Set(vectors.catalog.map((entry) => entry.type)).size,
    vectors.catalog.length,
  );
  assert.equal(
    new Set(vectors.catalog.map((entry) => entry.alias)).size,
    vectors.catalog.length,
  );
  for (const vector of vectors.cases) {
    if (vector.valid) {
      const entry = vectors.catalog.find(
        (candidate) => candidate.type === vector.parsed.type,
      );
      assert.ok(entry, vector.name);
      assert.equal(vector.parsed.type, entry.type, vector.name);
      assert.equal(vector.parsed.subjectType, entry.subjectType, vector.name);
    } else {
      assert.match(
        vector.errorCode,
        /^CURATION_ACTION_(?:COMMAND_INVALID|ALIAS_UNKNOWN|BODY_INVALID)$/,
        vector.name,
      );
    }
  }
});

test("Curation timeline fixture includes only APPEND actions and redacts owner payload", async () => {
  const fixtureValue = await fixture("curation-timeline.v1.json");
  const timelineTypes = new Set([
    "INTENT_NEXT_STEP",
    "PLANNING_ADD_TARGETS",
    "PLANNING_START_CURATING",
    "CURATION_ADD_TARGETS",
    "TARGET_RESEARCH_AGAIN",
  ]);
  const bodyTypes = new Set([
    "INTENT_NEXT_STEP",
    "PLANNING_ADD_TARGETS",
    "CURATION_ADD_TARGETS",
    "TARGET_RESEARCH_AGAIN",
  ]);
  const resultKinds = new Set([
    "INTENT_ACCEPTED",
    "TARGET_EXPANSION",
    "RESEARCH_STARTED",
    "TARGET_RESEARCHED",
  ]);
  for (const item of fixtureValue.items) {
    assert.ok(timelineTypes.has(item.action.type));
    assert.equal(
      "displayBody" in item,
      bodyTypes.has(item.action.type),
      item.action.type,
    );
    if (item.result) {
      assert.ok(resultKinds.has(item.result.kind));
    }
    assert.ok(item.result, `${item.action.type} must have a projected result`);
  }
  const serialized = JSON.stringify(fixtureValue);
  for (const forbidden of [
    "actorUserId",
    "sourceRefId",
    "requestHash",
    "candidateConfigurationId",
    "configurationHash",
    "selectionId",
    "inputSnapshot",
    "credential",
  ]) {
    assert.equal(
      serialized.includes(forbidden),
      false,
      `timeline leaked ${forbidden}`,
    );
  }
});

test("AgencyOrder customer actions fixture keeps the closed action space", async () => {
  const fixtureValue = await fixture("agency-order-customer-actions.v2.json");
  assert.equal(
    fixtureValue.schemaVersion,
    "vitlane.agency-order-customer-actions.v2",
  );
  const knownKinds = new Set([
    "PAY",
    "CANCEL_PRE_EFFECT",
    "CANCEL_DELAY_RULE",
    "REQUEST_REFUND",
  ]);
  assert.ok(fixtureValue.cases.length > 0);
  for (const entry of fixtureValue.cases) {
    assert.deepEqual(
      entry.projection.availableActions,
      entry.availableActions,
      entry.name,
    );
    for (const action of entry.availableActions) {
      assert.ok(knownKinds.has(action.kind), `${entry.name}: ${action.kind}`);
      if (["REQUEST_REFUND", "CANCEL_PRE_EFFECT", "CANCEL_DELAY_RULE"].includes(action.kind)) {
        const merchantOrders = new Set(entry.projection.merchantOrders.map((merchantOrder) => merchantOrder.id));
        for (const id of action.eligibleMerchantOrderIds ?? []) {
          assert.ok(merchantOrders.has(id), `${entry.name}: MerchantOrder ${id}`);
        }
      }
      if (action.kind === "REQUEST_REFUND") {
        assert.ok(!action.reasonCodes.includes("CHANGE_OF_MIND"), entry.name);
      }
    }
  }
});

test("Step 4 scoring contract conserves 100 and keeps missing axes distinct from zero", async () => {
  const v = await fixture("curation-step4.v1.json");
  for (const c of v.weightCases) {
    assert.equal(c.weights.reduce((a,b)=>a+b,0),100);
    const sum=c.importance.reduce((a,b)=>a+b,0);
    c.weights.forEach((weight,i)=>assert.ok(Math.abs(weight-c.importance[i]*100/sum)<1));
  }
  const a=v.assessmentCase;
  assert.equal(a.scores.reduce((sum,score,i)=>sum+score*a.weights[i],0),a.totalBasisPoints);
  assert.equal(Math.round(a.totalBasisPoints/100),a.totalScore);
  assert.equal(v.sorting.candidates[0].axisScore,undefined);
  assert.equal(v.sorting.candidates[1].axisScore,0);
});
