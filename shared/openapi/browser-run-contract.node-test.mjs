import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const directory = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(directory, "../..");
const requireFromWeb = createRequire(path.join(root, "web", "package.json"));
const { parseDocument } = requireFromWeb("yaml");

const documentPromise = readFile(path.join(directory, "v1.yaml"), "utf8").then(
  (source) => {
    const parsed = parseDocument(source, { prettyErrors: true, uniqueKeys: true });
    assert.deepEqual(parsed.errors, [], parsed.errors.map(String).join("\n"));
    return parsed.toJS();
  },
);

test("browser run starts only from a stored candidate and separates approvals", async () => {
  const document = await documentPromise;
  const create =
    document.paths[
      "/curations/{curationId}/catalog-research/candidates/{candidateId}/browser-runs"
    ].post;
  const createBody = create.requestBody.content["application/json"].schema;
  assert.equal(createBody.additionalProperties, false);
  assert.equal(Object.hasOwn(createBody, "properties"), false);

  const preparation = document.components.schemas.BrowserRunPreparationApprovalV1;
  assert.ok(preparation.required.includes("planRevision"));
  assert.ok(preparation.required.includes("quoteDigest"));
  assert.ok(preparation.required.includes("priceCeilingMinor"));
  assert.ok(preparation.required.includes("idempotencyKey"));
  assert.deepEqual(
    preparation.properties.allowedPreparationSteps.items.enum,
    ["SELECT_VARIANT", "SET_QUANTITY", "ADD_TO_CART", "OPEN_CHECKOUT_REVIEW"],
  );
  for (const forbidden of ["SUBMIT_PAYMENT", "ENTER_PASSWORD", "ENTER_OTP", "FILL_CARD"]) {
    assert.equal(
      preparation.properties.allowedPreparationSteps.items.enum.includes(forbidden),
      false,
    );
  }
});

test("resume needs a fresh sanitized observation and session hints remain untrusted", async () => {
  const document = await documentPromise;
  const observation = document.components.schemas.BrowserRunObservationCommandV1;
  assert.equal(observation.properties.sanitized.const, true);
  assert.equal(observation.properties.containsSensitiveData.const, false);
  assert.deepEqual(observation.properties.sessionStateHint.enum, [
    "authenticated",
    "anonymous",
    "unknown",
  ]);
  assert.match(observation.properties.sessionStateHint.description, /untrusted/);
  assert.match(
    document.paths["/browser-runs/{runId}/resume-requests"].post.description,
    /fresh sanitized observation/,
  );
});

test("result evidence preserves merchant observation and user report as distinct sources", async () => {
  const document = await documentPromise;
  const result = document.components.schemas.BrowserRunResultVerificationCommandV1;
  assert.deepEqual(result.properties.evidenceSource.enum, [
    "MERCHANT_OBSERVED",
    "USER_REPORTED",
  ]);
  const run = document.components.schemas.BrowserRunV1;
  assert.ok(run.properties.state.enum.includes("READY_FOR_USER_PAYMENT"));
  assert.equal(run.properties.controlOwner.enum.includes("USER"), true);
  assert.equal(
    run.properties.state.enum.includes("PAYMENT_AUTHORIZED"),
    false,
  );
});
