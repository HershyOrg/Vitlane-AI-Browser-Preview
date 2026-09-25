import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const requireFromWeb = createRequire(
  path.join(repositoryRoot, "web", "package.json"),
);
const { parseDocument } = requireFromWeb("yaml");

async function read(relativePath) {
  return readFile(path.join(repositoryRoot, relativePath), "utf8");
}

async function readJSON(relativePath) {
  return JSON.parse(await read(relativePath));
}

function canonicalizeFixture(value) {
  if (
    value === null ||
    typeof value === "boolean" ||
    typeof value === "number" ||
    typeof value === "string"
  ) {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalizeFixture).join(",")}]`;
  }
  assert.equal(typeof value, "object");
  return `{${Object.keys(value)
    .sort()
    .map(
      (key) =>
        `${JSON.stringify(key)}:${canonicalizeFixture(value[key])}`,
    )
    .join(",")}}`;
}

function sha256Wire(value) {
  return `sha256:${createHash("sha256").update(value, "utf8").digest("hex")}`;
}

function parseYAML(source) {
  const document = parseDocument(source, {
    prettyErrors: true,
    uniqueKeys: true,
  });
  assert.deepEqual(
    document.errors,
    [],
    `OpenAPI YAML parse failed: ${document.errors.map(String).join("\n")}`,
  );
  return document.toJS();
}

function pointerValue(root, reference) {
  assert.match(reference, /^#\//, `only local refs are allowed: ${reference}`);
  return reference
    .slice(2)
    .split("/")
    .map((part) => part.replaceAll("~1", "/").replaceAll("~0", "~"))
    .reduce((value, part) => {
      assert.ok(
        value && Object.hasOwn(value, part),
        `unresolved OpenAPI ref ${reference}`,
      );
      return value[part];
    }, root);
}

function walk(value, visit, location = "#") {
  if (!value || typeof value !== "object") return;
  visit(value, location);
  for (const [key, child] of Object.entries(value)) {
    walk(child, visit, `${location}/${key}`);
  }
}

function assertAllRefsResolve(document) {
  walk(document, (value, location) => {
    if (!Object.hasOwn(value, "$ref")) return;
    assert.equal(
      typeof value.$ref,
      "string",
      `${location}/$ref must be a string`,
    );
    pointerValue(document, value.$ref);
  });
}

function assertUniqueOperationIDs(document) {
  const seen = new Map();
  const methods = new Set([
    "get",
    "put",
    "post",
    "delete",
    "options",
    "head",
    "patch",
    "trace",
  ]);
  for (const [route, pathItem] of Object.entries(document.paths)) {
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!methods.has(method) || !operation.operationId) continue;
      assert.equal(
        seen.has(operation.operationId),
        false,
        `duplicate operationId ${operation.operationId} at ${method.toUpperCase()} ${route}; first seen at ${seen.get(operation.operationId)}`,
      );
      seen.set(operation.operationId, `${method.toUpperCase()} ${route}`);
    }
  }
}

function validate(schema, value, root, location = "$") {
  if (schema.$ref) {
    validate(pointerValue(root, schema.$ref), value, root, location);
    return;
  }
  for (const member of schema.allOf || []) {
    validate(member, value, root, location);
  }
  if (Object.hasOwn(schema, "const")) {
    assert.deepEqual(value, schema.const, `${location} violates const`);
  }
  if (schema.enum) {
    assert.ok(schema.enum.includes(value), `${location} is outside enum`);
  }

  switch (schema.type) {
    case "object": {
      assert.ok(
        value !== null && typeof value === "object" && !Array.isArray(value),
        `${location} must be an object`,
      );
      for (const key of schema.required || []) {
        assert.ok(
          Object.hasOwn(value, key),
          `${location}.${key} is required`,
        );
      }
      if (schema.additionalProperties === false) {
        for (const key of Object.keys(value)) {
          assert.ok(
            Object.hasOwn(schema.properties || {}, key),
            `${location}.${key} is not allowed`,
          );
        }
      }
      for (const [key, child] of Object.entries(value)) {
        if (schema.properties?.[key]) {
          validate(schema.properties[key], child, root, `${location}.${key}`);
        } else if (
          schema.additionalProperties &&
          typeof schema.additionalProperties === "object"
        ) {
          validate(schema.additionalProperties, child, root, `${location}.${key}`);
        }
      }
      break;
    }
    case "array":
      assert.ok(Array.isArray(value), `${location} must be an array`);
      if (schema.minItems !== undefined) {
        assert.ok(value.length >= schema.minItems, `${location} has too few items`);
      }
      if (schema.maxItems !== undefined) {
        assert.ok(value.length <= schema.maxItems, `${location} has too many items`);
      }
      value.forEach((child, index) => {
        if (schema.items) validate(schema.items, child, root, `${location}[${index}]`);
      });
      break;
    case "string":
      assert.equal(typeof value, "string", `${location} must be a string`);
      if (schema.minLength !== undefined) {
        assert.ok(value.length >= schema.minLength, `${location} is too short`);
      }
      if (schema.maxLength !== undefined) {
        assert.ok(value.length <= schema.maxLength, `${location} is too long`);
      }
      if (schema.pattern) {
        assert.match(value, new RegExp(schema.pattern), `${location} violates pattern`);
      }
      if (schema.format === "date-time") {
        assert.equal(Number.isNaN(Date.parse(value)), false, `${location} is not a date-time`);
      } else if (schema.format === "uuid") {
        assert.match(
          value,
          /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i,
          `${location} is not a UUID`,
        );
      } else if (schema.format === "uri") {
        assert.doesNotThrow(() => new URL(value), `${location} is not a URI`);
      }
      break;
    case "integer":
      assert.ok(Number.isInteger(value), `${location} must be an integer`);
      break;
    case "number":
      assert.equal(typeof value, "number", `${location} must be a number`);
      break;
    case "boolean":
      assert.equal(typeof value, "boolean", `${location} must be a boolean`);
      break;
    case "null":
      assert.equal(value, null, `${location} must be null`);
      break;
    default:
      break;
  }
  if (schema.minimum !== undefined) {
    assert.ok(value >= schema.minimum, `${location} is below minimum`);
  }
  if (schema.maximum !== undefined) {
    assert.ok(value <= schema.maximum, `${location} is above maximum`);
  }
}

function responseSchema(document, route, method, status) {
  const response = document.paths[route]?.[method]?.responses?.[status];
  assert.ok(response, `missing ${method.toUpperCase()} ${route} response ${status}`);
  return response.content?.["application/json"]?.schema;
}

const uuid = (suffix) => `00000000-0000-4000-8000-${suffix.padStart(12, "0")}`;
const address = (digit) => `0x${digit.repeat(40)}`;
const hash = (digit) => `0x${digit.repeat(64)}`;
const now = "2026-07-30T00:00:00Z";

function authorizationFixture() {
  return {
    id: uuid("10"),
    agencyOrderId: uuid("11"),
    authorization: {
      payer: address("1"),
      token: address("2"),
      passThroughAmount: "50000000",
      feeAmount: "500000",
      orderHash: hash("3"),
      merchantId: hash("4"),
      merchantRegistryVersion: 1,
      feeBps: 100,
      feeRecipient: address("5"),
      principalRecipient: address("6"),
      assuranceLevel: hash("7"),
      nonce: "123",
      payDeadline: 1785373200,
      refundAfter: 1785376800,
    },
    domain: {
      name: "Vitlane Settlement",
      version: "2",
      chainId: 91342,
      verifyingContract: address("4"),
    },
    signer: address("8"),
    typedDataHash: hash("9"),
    signature: `0x${"a".repeat(130)}`,
    createdAt: now,
  };
}

const openAPIPromise = read("shared/openapi/v1.yaml").then((source) => ({
  document: parseYAML(source),
}));

test("KR 상품 반응은 Variant 없는 원본 관찰·CAS·계정 좋아요 계약을 사용한다", async () => {
  const { document } = await openAPIPromise;
  const { unknownObservation: observation } = await readJSON("shared/openapi/fixtures/curation-step2.v1.json");
  const path = "/curations/{curationId}/catalog-research/candidates/{candidateId}/external-product/reaction";
  const operation = document.paths[path].put;
  const command = { schemaVersion: "vitlane.product-reaction.v1", productRef: observation.productRef, pinned: true, sentiment: "LIKE", expectedVersion: 0 };
  const schema = operation.requestBody.content["application/json"].schema;
  validate(schema, command, document);
  assert.throws(() => validate(schema, { ...command, variantId: "invented" }, document));
  assert.throws(() => validate(schema, { ...command, expectedVersion: -1 }, document));
  const reaction = { targetId: "target", candidateId: "candidate", productRef: observation.productRef, pinned: true, sentiment: "LIKE", version: 1, updatedAt: observation.observedAt };
  validate(responseSchema(document, path, "put", "200"), { schemaVersion: command.schemaVersion, reaction }, document);
  validate(document.components.schemas.ProductReactionV1, { ...reaction, pinned: false, sentiment: "NONE", version: 0, updatedAt: "0001-01-01T00:00:00Z" }, document);
  validate(responseSchema(document, "/account/liked-variants", "get", "200"), { candidates: [], products: [{ curationId: "curation", candidateId: "candidate", observation, updatedAt: observation.observedAt }] }, document);
});

test("OpenAPI YAML은 중복 key 없이 parse되고 모든 local $ref와 operationId가 유효하다", async () => {
  const { document } = await openAPIPromise;
  assert.equal(document.openapi, "3.1.0");
  assert.ok(document.paths && document.components?.schemas);
  assertAllRefsResolve(document);
  assertUniqueOperationIDs(document);
});

test("Phase 7 운영 readback·세션·UNKNOWN 판정은 감사 경계를 가진 계약으로 고정된다", async () => {
  const { document } = await openAPIPromise;
  const expected = new Map([
    ["/admin/ops/health", "getOperatorOpsHealth"],
    ["/admin/ops/heartbeats", "getOperatorOpsHeartbeats"],
    ["/admin/ops/samples", "getOperatorOpsSamples"],
    ["/admin/ops/sessions", "listActiveAuthSessions"],
    [
      "/admin/ops/users/{userId}/session-revocations",
      "revokeUserAuthSessions",
    ],
    [
      "/admin/managed-runner/reservations/{reservationId}/resolutions",
      "resolveManagedRunnerUnknownReservation",
    ],
  ]);
  for (const [route, operationID] of expected) {
    const pathItem = document.paths[route];
    assert.ok(pathItem, `OpenAPI missing ${route}`);
    assert.ok(
      Object.values(pathItem).some(
        (operation) => operation?.operationId === operationID,
      ),
      `OpenAPI missing ${operationID}`,
    );
  }

  const healthResponses = document.paths["/admin/ops/health"].get.responses;
  assert.equal(
    healthResponses["200"].content["application/json"].schema.$ref,
    "#/components/schemas/OperatorOpsHealth",
  );
  assert.equal(
    healthResponses["503"].content["application/json"].schema.$ref,
    "#/components/schemas/OperatorOpsHealth",
    "core unavailable도 degraded와 같은 readback shape를 유지해야 한다",
  );
  assert.deepEqual(
    document.components.schemas.OperatorOpsHealth.properties.status.enum,
    ["ready", "degraded", "unavailable"],
  );
  assert.equal(
    document.components.schemas.OperatorOpsSamples.properties.points.maxItems,
    600,
    "시계열 응답은 화면 표시용 bounded payload여야 한다",
  );
  assert.equal(
    document.paths["/admin/ops/samples"].get.parameters[0].schema.maximum,
    720,
    "시계열 조회 기간은 30일로 제한해야 한다",
  );
  assert.equal(
    document.components.schemas.OperatorOpsHealth.properties.host.$ref,
    "#/components/schemas/OperatorOpsHostFacts",
  );

  const revokeRequest = document.paths[
    "/admin/ops/users/{userId}/session-revocations"
  ].post.requestBody.content["application/json"].schema;
  assert.deepEqual(revokeRequest.required, ["reasonDetail"]);
  assert.equal(revokeRequest.properties.reasonDetail.minLength, 8);

  const resolutionRequest = document.paths[
    "/admin/managed-runner/reservations/{reservationId}/resolutions"
  ].post.requestBody.content["application/json"].schema;
  assert.deepEqual(
    resolutionRequest.required,
    ["outcome", "reasonDetail", "evidenceReference"],
  );
  assert.deepEqual(
    resolutionRequest.properties.outcome.enum,
    ["SETTLED", "RELEASED"],
  );
});

test("retired ResearchSubmission public schema는 노출되지 않는다", async () => {
  const { document } = await openAPIPromise;
  assert.equal(
    Object.hasOwn(document.components.schemas, "ResearchSubmission"),
    false,
  );
  assert.equal(document.info.description.includes("Agent OAuth"), false);
  const errorCodes =
    document.components.schemas.ErrorResponse.properties.error.properties.code.enum;
  for (const retired of [
    "AGENT_CONNECTION_COMMAND_INVALID",
    "AGENT_CONNECTION_CONFLICT",
    "AGENT_CONNECTION_VERSION_CONFLICT",
    "AGENT_CONNECTION_IDEMPOTENCY_CONFLICT",
  ]) {
    assert.equal(errorCodes.includes(retired), false, `${retired} is retired`);
  }
});

test("Wallet 등록·소유권·MockDojang KYC API가 하나의 새 계약만 노출된다", async () => {
  const { document } = await openAPIPromise;
  const expected = new Map([
    ["/account/wallet-registration-attempts", "createWalletRegistrationAttempt"],
    ["/account/wallet-registration-attempts/{attemptId}/complete", "completeWalletRegistrationAttempt"],
    ["/account/wallets/{walletId}/deregister", "deregisterWallet"],
    ["/account/wallets/{walletId}/kyc-cases", "startKYCVerification"],
    ["/account/kyc-cases/{caseId}/check", "checkKYC"],
  ]);
  for (const [route, operationID] of expected) {
    const pathItem = document.paths[route];
    assert.ok(pathItem, `OpenAPI missing ${route}`);
    assert.ok(
      Object.values(pathItem).some((operation) => operation?.operationId === operationID),
      `OpenAPI missing ${operationID}`,
    );
  }
  for (const legacy of [
    "/account/wallet-challenges",
    "/account/wallets/verify",
    "/account/wallet-connection-attempts",
    "/account/wallet-preference/default",
    "/account/wallets/{walletId}/disconnect",
  ]) {
    assert.equal(Object.hasOwn(document.paths, legacy), false, `OpenAPI contains ${legacy}`);
  }

  const schemas = document.components.schemas;
  assert.deepEqual(
    schemas.Wallet.properties.registrationStatus.enum,
    ["REGISTERED", "DEREGISTERED"],
  );
  assert.deepEqual(
    schemas.WalletRegistrationAttempt.properties.status.enum,
    ["PENDING", "COMPLETED", "EXPIRED", "LOCKED", "CANCELLED", "SUPERSEDED"],
  );
  assert.equal(
    schemas.WalletOwnershipProof.properties.method.const,
    "EIP191_PERSONAL_SIGN",
  );
  assert.match(
    schemas.WalletOwnershipProof.properties.validUntil.description,
    /정확히 24시간/,
  );
  assert.deepEqual(
    schemas.KYCVerificationCase.properties.providerKind.enum,
    ["DOJANG", "MOCK_DOJANG"],
  );
  assert.deepEqual(
    schemas.KYCVerificationCase.properties.externalEffect.enum,
    ["LIVE", "SIMULATED"],
  );
  assert.match(
    schemas.WalletKYCProjection.properties.disclosure.description,
    /실제 외부 신원 확인이 아님/,
  );
  assert.equal(
    schemas.WalletKYCProjection.properties.failureCode.type,
    "string",
  );
  assert.equal(
    schemas.WalletKYCProjection.properties.retryable.type,
    "boolean",
  );
});

test("Wallet attempt와 AgencyOrder 정산·영수증의 대표 runtime 응답이 OpenAPI schema를 만족한다", async () => {
  const { document } = await openAPIPromise;
  const attempt = {
    id: uuid("1"),
    address: address("1"),
    accountId: `eip155:91342:${address("1")}`,
    chainId: "eip155:91342",
    status: "CANCELLED",
    message: "Vitlane wallet registration",
    messageHash: hash("1"),
    nonce: "nonce-1",
    expiresAt: "2026-07-30T00:10:00Z",
  };
  validate(
    responseSchema(
      document,
      "/account/wallet-registration-attempts",
      "post",
      "201",
    ),
    { attempt, replay: false },
    document,
  );

  const ownershipProof = {
    id: uuid("16"),
    userId: uuid("13"),
    walletId: uuid("15"),
    registrationAttemptId: uuid("1"),
    address: address("1"),
    accountId: `eip155:91342:${address("1")}`,
    chainId: "eip155:91342",
    origin: "https://vitlane.example",
    method: "EIP191_PERSONAL_SIGN",
    messageHash: hash("1"),
    verifiedAt: now,
    validUntil: "2026-07-31T00:00:00Z",
  };
  const latestWallet = {
    wallet: {
      id: uuid("15"),
      userId: uuid("13"),
      address: address("1"),
      accountId: `eip155:91342:${address("1")}`,
      chainId: "eip155:91342",
      registrationStatus: "REGISTERED",
      currentOwnershipProofId: uuid("16"),
      isDefault: true,
      registeredAt: now,
      createdAt: now,
      updatedAt: now,
    },
    ownership: {
      status: "VALID",
      proofId: uuid("16"),
      verifiedAt: now,
      validUntil: "2026-07-31T00:00:00Z",
      nextAction: { kind: "START_KYC" },
    },
    kyc: {
      eligibility: "NONE",
      actionEligible: false,
      providerKind: "MOCK_DOJANG",
      externalEffect: "SIMULATED",
      disclosure: "TEST/SIMULATED이며 실제 외부 신원 확인이 아님",
      nextAction: { kind: "START_KYC" },
    },
    actions: {
      canSetDefault: false,
      canDeregister: true,
      canReauthenticate: false,
      canStartKYC: true,
      canCheckKYC: false,
    },
  };
  validate(
    responseSchema(
      document,
      "/account/wallet-registration-attempts/{attemptId}/complete",
      "post",
      "200",
    ),
    {
      registration: {
        walletId: uuid("15"),
        ownershipProofId: uuid("16"),
        address: address("1"),
        accountId: `eip155:91342:${address("1")}`,
        chainId: "eip155:91342",
        verifiedAt: now,
        validUntil: "2026-07-31T00:00:00Z",
      },
      latestWallet,
      ownershipProof,
      replay: false,
    },
    document,
  );

  const authorization = authorizationFixture();
  validate(
    responseSchema(
      document,
      "/agencyOrder/{agencyOrderId}/settlement-authorizations",
      "post",
      "201",
    ),
    authorization,
    document,
  );

  validate(
    responseSchema(document, "/agencyOrder/{agencyOrderId}/settlement", "get", "200"),
    {
      authorization,
      payment: {
        id: uuid("20"),
        agencyOrderId: uuid("11"),
        orderHash: hash("3"),
        chainId: 91342,
        settlementAddress: address("4"),
        payer: address("1"),
        amountBaseUnits: "50000000",
        state: "AUTHORIZED",
        createdAt: now,
        updatedAt: now,
      },
    },
    document,
  );

  const receipt = {
    id: uuid("21"),
    agencyOrderId: uuid("11"),
    settlementPaymentId: uuid("20"),
    kind: "TEST",
    paymentRail: "GIWA",
    providerEnvironment: "TESTNET",
    asset: "TVITUSD",
    economicEffect: "NO_REAL_VALUE",
    merchantExecutionMode: "SIMULATED_NO_EFFECT",
    executionProfileHash: "0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732",
    legalSale: false,
    terminalState: "COMPLETED_ALL",
    terminalTxHash: hash("7"),
    receiptHash: hash("8"),
    payload: { schemaVersion: "vitlane.agency-order-receipt.v4" },
    createdAt: now,
  };
  validate(
    responseSchema(document, "/agencyOrder/{agencyOrderId}/receipt", "get", "200"),
    { receipt },
    document,
  );
});

test("Web client 타입은 Wallet attempt와 AgencyOrder terminal projection을 보존한다", async () => {
  const accountAPI = await read("web/src/products/account/infra/accountApi.ts");
  assert.match(accountAPI, /\|\s*"CANCELLED"/);
  assert.match(accountAPI, /failureCode\?: string/);
  assert.match(accountAPI, /retryable\?: boolean/);

  const agencyOrderAPI = await read("web/src/products/ordering/infra/agencyOrderApi.ts");
  for (const field of [
    "export type AgencyOrderProjection",
    "export type MerchantOrderSummary",
    "export type CustomerNotice",
    "export type AgencyOrderReceipt",
    'kind: "TEST" | "LIVE_ORDER_RECORD"',
    "providerEnvironment:",
    "executionProfileHash:",
    "legalSale: boolean",
    '"/api/v1/agencyOrder"',
  ]) {
    assert.ok(agencyOrderAPI.includes(field), `Web AgencyOrder API missing ${field}`);
  }
  assert.equal(agencyOrderAPI.includes("/purchases"), false);
});

test("AgencyOrder가 정산·transaction·환불·영수증의 유일한 공개 주문 API다", async () => {
  const { document } = await openAPIPromise;
  const operations = [
    ["get", "/settlement/config", "getSettlementConfig"],
    ["get", "/agencyOrder", "listAgencyOrders"],
    ["get", "/agencyOrder/{agencyOrderId}", "getAgencyOrder"],
    ["post", "/agencyOrder/{agencyOrderId}/settlement-authorizations", "createAgencyOrderSettlementAuthorization"],
    ["post", "/agencyOrder/{agencyOrderId}/wallet-transactions", "submitAgencyOrderWalletTransaction"],
    ["post", "/agencyOrder/{agencyOrderId}/settlement-transactions", "submitAgencyOrderPayTransaction"],
    ["post", "/agencyOrder/{agencyOrderId}/refund-intents", "createAgencyOrderRefundIntent"],
    ["post", "/agencyOrder/{agencyOrderId}/refund-transactions", "submitAgencyOrderRefundTransaction"],
    ["get", "/agencyOrder/{agencyOrderId}/settlement", "getAgencyOrderSettlement"],
    ["get", "/agencyOrder/{agencyOrderId}/receipt", "getAgencyOrderReceipt"],
    ["get", "/admin/procurement/queue", "listProcurementQueue"],
  ];
  for (const [method, route, operationID] of operations) {
    assert.equal(
      document.paths[route]?.[method]?.operationId,
      operationID,
      `${method.toUpperCase()} ${route} does not match runtime routing`,
    );
  }

  for (const route of Object.keys(document.paths)) {
    assert.equal(route.startsWith("/purchases"), false, `legacy Purchase route remains: ${route}`);
    assert.equal(route.startsWith("/purchase-origin-requests"), false, `legacy OriginRequest route remains: ${route}`);
    assert.equal(route.startsWith("/admin/phase5/fulfillments"), false, `legacy Fulfillment route remains: ${route}`);
  }
  assert.equal(
    document.components.responses.SettlementQuoteStale
      .content["application/json"].example.error.code,
    "SETTLEMENT_QUOTE_STALE",
  );

  const errorCodes =
    document.components.schemas.ErrorResponse.properties.error.properties.code.enum;
  for (const code of [
    "WALLET_REGISTRATION_ATTEMPT_EXPIRED",
    "WALLET_OWNERSHIP_PROOF_NOT_FRESH",
    "KYC_PROVIDER_UNAVAILABLE",
    "KYC_CREDENTIAL_INVALID",
    "AGENCY_ORDER_NOT_FOUND",
    "AGENCY_ORDER_PROCESS_STATE_INVALID",
    "AGENCY_ORDER_EXECUTION_NOT_FOUND",
    "SETTLEMENT_QUOTE_STALE",
    "SETTLEMENT_AUTHORIZATION_EXPIRED",
    "SETTLEMENT_PAYMENT_STATE_INVALID",
    "CHAIN_TRANSACTION_INVALID",
    "AGENCY_ORDER_RECEIPT_NOT_FOUND",
  ]) {
    assert.ok(errorCodes.includes(code), `ErrorResponse missing ${code}`);
  }
});

test("AgencyOrder capability는 기능 비활성 상태에서도 Web이 안전하게 진입을 차단할 계약을 제공한다", async () => {
  const { document } = await openAPIPromise;
  const operation = document.paths["/agency-order-capability"]?.get;
  assert.equal(operation?.operationId, "getAgencyOrderCapability");
  assert.equal(
    operation.responses["200"].content["application/json"].schema.$ref,
    "#/components/schemas/AgencyOrderCapabilityResponse",
  );

  const capability = document.components.schemas.AgencyOrderCapabilityResponse;
  assert.deepEqual(capability.required, ["schemaVersion", "capability"]);
  assert.deepEqual(capability.properties.capability.properties.state.enum, [
    "READY",
    "UNAVAILABLE",
  ]);
  const capabilityBody = capability.properties.capability;
  assert.ok(capabilityBody.required.includes("capabilityRevision"));
  const rails = capabilityBody.properties.paymentRails;
  assert.deepEqual(rails.required, ["tvitusd", "paypalSandbox", "paypalLive"]);
  for (const rail of rails.required) {
    assert.equal(
      rails.properties[rail].$ref,
      "#/components/schemas/AgencyOrderPaymentRailCapability",
    );
  }
  const rail = document.components.schemas.AgencyOrderPaymentRailCapability;
  assert.ok(rail.required.includes("orderIssueState"));
  assert.ok(rail.required.includes("paymentInitiationState"));
  assert.ok(rail.required.includes("providerEnvironment"));
  assert.ok(rail.required.includes("economicEffect"));
  assert.deepEqual(rail.properties.paymentInitiationState.enum, [
    "READY",
    "PAUSED",
    "UNAVAILABLE",
  ]);
  assert.deepEqual(rail.properties.paymentMethod.enum, [
    "TVITUSD",
    "PAYPAL_SANDBOX",
    "PAYPAL_LIVE",
  ]);
});

test("PayPal Live control은 조회·kill만 공개하고 재활성화 HTTP 계약을 만들지 않는다", async () => {
  const { document } = await openAPIPromise;
  assert.equal(document.paths["/admin/liveControl"]?.get?.operationId, "getLiveControl");
  assert.equal(document.paths["/admin/liveControl/kill"]?.post?.operationId, "killLiveControl");
  assert.equal(document.paths["/admin/liveControl/reactivate"], undefined);

  const response = document.components.schemas.LiveControlResponse;
  assert.equal(response.properties.schemaVersion.const, "vitlane.live-control.v1");
  assert.deepEqual(response.properties.liveControl.required, [
    "version",
    "orderIssue",
    "paypalMoney",
    "merchantEffect",
    "changedAt",
    "changedBy",
    "reason",
  ]);
  const errorCodes = document.components.schemas.ErrorResponse.properties.error
    .properties.code.enum;
  for (const code of [
    "PAYPAL_RAIL_UNAVAILABLE",
    "PAYPAL_LIVE_KILLED",
    "LIVE_CONTROL_UNAVAILABLE",
    "LIVE_CONTROL_CONFIRMATION_INVALID",
    "LIVE_CONTROL_VERSION_CONFLICT",
    "LIVE_CONTROL_MUTATION_FAILED",
  ]) {
    assert.ok(errorCodes.includes(code), `ErrorResponse missing ${code}`);
  }
});

test("OrderSheet paymentSelection은 staged PayPal profile을 OpenAPI에도 보존한다", async () => {
  const { document } = await openAPIPromise;
  assert.deepEqual(
    document.components.schemas.OrderSheet.properties.paymentSelection.enum,
    ["TVITUSD", "PAYPAL_SANDBOX", "PAYPAL_LIVE"],
  );
});

test("Curation action·Selection·AgencyOrder hard cutover 계약만 노출한다", async () => {
  const { document } = await openAPIPromise;
  const expectedOperations = [
    ["get", "/curations/{curationId}/available-actions", "getAvailableCurationActions"],
    ["get", "/curations/{curationId}/actions", "listCurationActions"],
    ["put", "/curations/{curationId}/actions/{actionId}/target-remove", "executeTargetRemoveAction"],
    ["get", "/curations/{curationId}/workspace", "getCurationWorkspace"],
    ["get", "/curations/{curationId}/cart", "getCurationCart"],
    ["post", "/curations/{curationId}/selections", "createCurationSelection"],
    ["put", "/curations/{curationId}/selections/{selectionId}", "updateCurationSelection"],
    ["delete", "/curations/{curationId}/selections/{selectionId}", "removeCurationSelection"],
  ];
  for (const [method, route, operationID] of expectedOperations) {
    assert.equal(
      document.paths[route]?.[method]?.operationId,
      operationID,
      `${method.toUpperCase()} ${route} is not the canonical operation`,
    );
  }

  for (const legacyRoute of [
    "/shopping-carts",
    "/shopping-plans/{planId}/cart",
    "/shopping-plans/{planId}/cart/checkout",
    "/shopping-plans/{planId}/research",
  ]) {
    assert.equal(
      Object.hasOwn(document.paths, legacyRoute),
      false,
      `legacy route remains: ${legacyRoute}`,
    );
  }
  assert.equal(
    Object.keys(document.paths).some((route) => route.startsWith("/purchases") || route.startsWith("/purchase-origin-requests")),
    false,
    "Purchase/OriginRequest public API must be absent",
  );

  const schemas = document.components.schemas;
  for (const legacySchema of [
    "ShoppingCart",
    "ShoppingCartItem",
    "ShoppingCartItemRequest",
    "PlanCartTracking",
  ]) {
    assert.equal(
      Object.hasOwn(schemas, legacySchema),
      false,
      `legacy schema remains: ${legacySchema}`,
    );
  }
  assert.deepEqual(schemas.Curation.properties.phase.enum, [
    "PLANNING",
    "CURATING",
  ]);
  assert.deepEqual(
    Object.keys(schemas.ShoppingPlan.properties).sort(),
    [
      "budgetRequest",
      "createdAt",
      "executionMode",
      "id",
      "locationContext",
      "originalIntent",
      "planningMode",
      "researchScope",
      "totalBudget",
      "userId",
    ],
    "ShoppingPlan must remain an immutable source snapshot",
  );
  // The recent-plans list and its PlanSummary schema were removed (ADR-0079);
  // the sidebar list carries only an ID, a title and a creation time.
  assert.equal(schemas.PlanSummary, undefined, "PlanSummary was removed with the recent-plans list");
  assert.deepEqual(
    schemas.CurationListItem.required,
    ["curationId", "intentSummary", "createdAt"],
    "The sidebar list row stays an ID, a title and a creation time",
  );
  assert.deepEqual(schemas.CurationRun.properties.status.enum, [
    "REQUESTED",
    "MATERIALIZING",
    "COMPLETED",
    "FAILED",
    "CANCELLED",
  ]);
  assert.deepEqual(schemas.JourneyStep.properties.key.enum, [
    "INTENT",
    "CURATION",
  ]);
  assert.equal(schemas.PlanJourney.properties.steps.minItems, 2);
  assert.equal(schemas.PlanJourney.properties.steps.maxItems, 2);
  assert.deepEqual(
    schemas.PlanJourney.properties.currentStage.enum,
    ["PLANNING", "READY", "RESEARCHING", "REVIEWING", "CURATING"],
  );
  assert.equal(
    JSON.stringify(schemas.PlanJourney).includes("PURCHASE"),
    false,
  );

  assert.deepEqual(
    Object.keys(schemas.CartView.properties).sort(),
    ["curationId", "selections", "total", "updatedAt", "warnings"],
  );
  for (const lifecycleField of [
    "status",
    "state",
    "eligible",
    "eligibility",
    "canCheckout",
  ]) {
    assert.equal(
      Object.hasOwn(schemas.CartView.properties, lifecycleField),
      false,
      `CartView cannot own ${lifecycleField}`,
    );
  }
  assert.equal(
    schemas.CurationWorkspace.properties.agencyOrderTrace.items.$ref,
    "#/components/schemas/CurationAgencyOrderTrace",
  );
  assert.equal(Object.hasOwn(schemas.CurationWorkspace.properties, "purchaseTrace"), false);
});

test("ADR-0026 parser, timeline과 snapshot golden vector가 같은 revision이다", async () => {
  const { document } = await openAPIPromise;
  const [
    parserFixture,
    timelineFixture,
    selectionFixture,
  ] = await Promise.all([
    readJSON("shared/openapi/fixtures/curation-action-command-parser.v1.json"),
    readJSON("shared/openapi/fixtures/curation-timeline.v1.json"),
    readJSON("shared/openapi/fixtures/curation-selection-snapshot-hash.v1.json"),
  ]);

  const actionTypes = document.components.schemas.CurationActionType.enum;
  const actionAliases =
    document.components.schemas.CurationActionDescriptor.properties.alias.enum;
  const descriptorTypes = parserFixture.catalog.map((entry) => entry.type);
  assert.equal(
    descriptorTypes.every((type) => actionTypes.includes(type)),
    true,
    "every current command descriptor must remain a valid action type",
  );
  assert.deepEqual(
    parserFixture.catalog.map((entry) => entry.alias),
    actionAliases,
  );
  for (const vector of parserFixture.cases.filter((entry) => entry.valid)) {
    const catalog = parserFixture.catalog.find(
      (entry) => entry.type === vector.parsed.type,
    );
    assert.ok(catalog, `parser fixture type is not catalogued: ${vector.name}`);
    assert.equal(vector.parsed.type, catalog.type);
    assert.equal(vector.parsed.subjectType, catalog.subjectType);
  }
  assert.equal(
    document.components.schemas.CurationWorkspace.properties.timeline.items.$ref,
    "#/components/schemas/CurationTimelineItem",
  );
  assert.deepEqual(
    document.components.schemas.CurationWorkspace.properties.coverage.enum,
    ["NONE", "PARTIAL", "COMPLETE"],
  );
  const timelineAction =
    document.components.schemas.CurationTimelineAction;
  const timelineActionProperties =
    timelineAction.properties;
  assert.equal(timelineAction.additionalProperties, false);
  assert.deepEqual(
    [...timelineAction.required].sort(),
    [
      "id",
      "curationId",
      "type",
      "phaseAtRequest",
      "subjectType",
      "effectKind",
      "expectedCurationVersion",
      "createdAt",
    ].sort(),
  );
  assert.deepEqual(
    Object.keys(timelineActionProperties).sort(),
    [
      ...timelineAction.required,
      "requestedTransitionTo",
      "subjectId",
    ].sort(),
  );
  for (const forbidden of [
    "actorUserId",
    "sourceRefType",
    "sourceRefId",
    "requestHash",
  ]) {
    assert.equal(
      Object.hasOwn(timelineActionProperties, forbidden),
      false,
      `CurationTimelineAction exposes ${forbidden}`,
    );
  }
  const timelineItem = document.components.schemas.CurationTimelineItem;
  assert.equal(timelineItem.additionalProperties, false);
  assert.deepEqual(timelineItem.required, ["action"]);
  assert.deepEqual(
    Object.keys(timelineItem.properties).sort(),
    ["action", "displayBody", "result"].sort(),
  );
  const timelineResult = document.components.schemas.CurationTimelineResult;
  assert.equal(timelineResult.additionalProperties, false);
  assert.deepEqual(
    [...timelineResult.required].sort(),
    ["kind", "summary", "occurredAt"].sort(),
  );
  assert.deepEqual(
    timelineResult.properties.kind.enum,
    [
      "INTENT_ACCEPTED",
      "TARGET_EXPANSION",
      "RESEARCH_STARTED",
      "TARGET_RESEARCHED",
    ],
  );
  assert.deepEqual(
    Object.keys(timelineResult.properties).sort(),
    ["kind", "summary", "occurredAt", "diff"].sort(),
  );
  const timelineDiff = document.components.schemas.CurationTimelineDiff;
  assert.equal(timelineDiff.additionalProperties, false);
  assert.deepEqual(
    Object.keys(timelineDiff.properties).sort(),
    ["added", "changed", "removed"].sort(),
  );
  const transcriptActionTypes = new Set([
    "INTENT_NEXT_STEP",
    "PLANNING_ADD_TARGETS",
    "PLANNING_START_CURATING",
    "CURATION_ADD_TARGETS",
    "TARGET_RESEARCH_AGAIN",
  ]);
  for (const item of timelineFixture.items) {
    assert.ok(
      transcriptActionTypes.has(item.action.type),
      `timeline contains PATCH_ONLY or unknown ${item.action.type}`,
    );
    assert.ok(
      item.result,
      `timeline omits result for append action ${item.action.type}`,
    );
  }

  const selectionCanonical = canonicalizeFixture(selectionFixture.value);
  assert.equal(selectionCanonical, selectionFixture.canonicalPreimage);
  assert.equal(
    sha256Wire(selectionCanonical),
    selectionFixture.snapshotHash,
  );
});

test("선택적 Curation 예산은 Init snapshot과 활성 원장을 분리한다", async () => {
  const document = parseYAML(await read("shared/openapi/v1.yaml"));
  const s = document.components.schemas;
  validate(s.InitialCurationBudgetV1, { schemaVersion: "vitlane.curation-budget.v1", currency: "USD", totalAmount: null, allocationMode: "AUTO" }, document);
  validate(s.CurationBudgetV1, { schemaVersion: "vitlane.curation-budget.v1", version: 4, researchVersion: 3, enabled: true, currency: "USD", totalAmount: "100.01", allocations: [
    { targetId: "e5000000-0000-4000-8000-000000000001", quantity: 2, amount: "60.00", minimumUnitAmount: "10.00" },
    { targetId: "e5000000-0000-4000-8000-000000000002", quantity: 1, amount: "40.01" },
  ] }, document);
  for (const inputMode of ["AUTO", "EXPLICIT"]) validate(s.InitialCurationBudgetV1, { schemaVersion: "vitlane.curation-budget.v1", inputMode, currency: "KRW", totalAmount: null, allocationMode: "AUTO" }, document);
  assert.deepEqual(s.InitialCurationBudgetV1.properties.inputMode.enum, ["AUTO", "EXPLICIT"]);
  assert(s.CurationBudgetCommandV1.required.includes("expectedVersion"));
  assert(s.CurationBudgetCommandV1.required.includes("commandId"));
  assert.equal(s.ShoppingPlan.properties.budgetRequest.$ref, "#/components/schemas/InitialCurationBudgetV1");
  assert.equal(Object.hasOwn(s.CurationBudgetV1.properties, "unallocated"), false);
});
