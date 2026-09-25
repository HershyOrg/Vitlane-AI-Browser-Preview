import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const openapiDirectory = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(openapiDirectory, "../..");
const requireFromWeb = createRequire(
  path.join(repositoryRoot, "web", "package.json"),
);
const { parseDocument } = requireFromWeb("yaml");

const documentPromise = readFile(
  path.join(openapiDirectory, "v1.yaml"),
  "utf8",
).then((source) => {
  const parsed = parseDocument(source, {
    prettyErrors: true,
    uniqueKeys: true,
  });
  assert.deepEqual(
    parsed.errors,
    [],
    `OpenAPI YAML parse failed: ${parsed.errors.map(String).join("\n")}`,
  );
  return parsed.toJS();
});

test("protected HTTP API accepts one browser cookie or one native bearer", async () => {
  const document = await documentPromise;

  assert.deepEqual(document.security, [
    { cookieAuth: [] },
    { bearerAuth: [] },
  ]);
  assert.deepEqual(document.components.securitySchemes.bearerAuth, {
    type: "http",
    scheme: "bearer",
    bearerFormat: "opaque AuthSession",
    description:
      "모바일 handoff exchange 또는 local·test 개발 세션에서 발급한 Vitlane opaque AuthSession token. Google/provider access token이나 MCP capability token이 아니며 cookieAuth와 동시에 보내지 않는다.",
  });
  assert.equal(document.paths["/me"].get.security, undefined);
});

test("mobile Google handoff keeps PKCE and one-time exchange as public protocol endpoints", async () => {
  const document = await documentPromise;
  const expected = new Map([
    ["/auth/mobile/google/start", ["get", "beginMobileGoogleLogin"]],
    ["/auth/mobile/complete", ["get", "completeMobileLogin"]],
    ["/auth/mobile/exchange", ["post", "exchangeMobileLogin"]],
  ]);

  for (const [route, [method, operationId]] of expected) {
    const operation = document.paths[route]?.[method];
    assert.ok(operation, `missing ${method.toUpperCase()} ${route}`);
    assert.equal(operation.operationId, operationId);
    assert.deepEqual(operation.security, []);
  }

  const start = document.paths["/auth/mobile/google/start"].get;
  const startParameters = Object.fromEntries(
    start.parameters.map((parameter) => [parameter.name, parameter]),
  );
  assert.deepEqual(Object.keys(startParameters).sort(), [
    "code_challenge",
    "redirect_uri",
  ]);
  assert.equal(startParameters.redirect_uri.required, true);
  assert.equal(startParameters.code_challenge.required, true);
  assert.equal(startParameters.code_challenge.schema.minLength, 43);
  assert.equal(startParameters.code_challenge.schema.maxLength, 43);
  assert.deepEqual(Object.keys(start.responses).sort(), [
    "302",
    "400",
    "429",
    "503",
  ]);

  const complete = document.paths["/auth/mobile/complete"].get;
  assert.match(
    complete.responses["303"].description,
    /code 또는 error/,
  );

  const exchange = document.paths["/auth/mobile/exchange"].post;
  assert.equal(
    exchange.requestBody.content["application/json"].schema.$ref,
    "#/components/schemas/MobileAuthExchangeRequest",
  );
  assert.equal(
    exchange.responses["201"].content["application/json"].schema.$ref,
    "#/components/schemas/MobileAuthSession",
  );
  assert.deepEqual(Object.keys(exchange.responses).sort(), [
    "201",
    "400",
    "401",
    "409",
  ]);
});

test("mobile auth schemas expose only the opaque Vitlane session grant", async () => {
  const document = await documentPromise;
  const schemas = document.components.schemas;
  const request = schemas.MobileAuthExchangeRequest;
  const response = schemas.MobileAuthSession;

  assert.equal(request.additionalProperties, false);
  assert.deepEqual(request.required, ["code", "verifier"]);
  assert.equal(request.properties.verifier.minLength, 43);
  assert.equal(request.properties.verifier.maxLength, 128);

  assert.equal(response.additionalProperties, false);
  assert.deepEqual(response.required, ["sessionToken", "expiresAt", "user"]);
  assert.deepEqual(Object.keys(response.properties).sort(), [
    "expiresAt",
    "sessionToken",
    "user",
  ]);
  for (const forbidden of [
    "accessToken",
    "refreshToken",
    "googleToken",
    "clientSecret",
  ]) {
    assert.equal(
      Object.hasOwn(response.properties, forbidden),
      false,
      `mobile session leaked ${forbidden}`,
    );
  }

  assert.ok(
    schemas.AuthenticationCapabilities.required.includes(
      "mobileGoogleEnabled",
    ),
  );
  assert.equal(
    schemas.AuthenticationCapabilities.properties.mobileGoogleEnabled.type,
    "boolean",
  );

  const errorCodes =
    schemas.ErrorResponse.properties.error.properties.code.enum;
  for (const code of [
    "AUTH_PROVIDER_FAILED",
    "MOBILE_AUTH_INVALID",
    "MOBILE_AUTH_EXPIRED",
    "DEV_SESSION_MODE_INVALID",
  ]) {
    assert.ok(errorCodes.includes(code), `missing mobile auth error ${code}`);
  }
  assert.equal(
    errorCodes.includes("MOBILE_AUTH_CONSUMED"),
    false,
    "handoff replay must not disclose a distinct public reason",
  );
});

test("development auth selects cookie or bearer without changing production auth", async () => {
  const document = await documentPromise;
  const operation = document.paths["/dev/auth/session"].post;
  const request = operation.requestBody.content["application/json"].schema;
  const response = operation.responses["201"].content["application/json"].schema;

  assert.deepEqual(request.properties.sessionMode.enum, ["cookie", "bearer"]);
  assert.equal(request.properties.sessionMode.default, "cookie");
  assert.deepEqual(response.oneOf, [
    { $ref: "#/components/schemas/DevelopmentCookieSession" },
    { $ref: "#/components/schemas/MobileAuthSession" },
  ]);
  assert.ok(operation.responses["400"]);
  assert.ok(operation.responses["404"]);
});
