import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, "../..");
const sourcePath = path.join(
  webRoot,
  "src/shared/ui/design-system/tokens.source.json",
);
const cssPath = path.join(
  webRoot,
  "src/shared/ui/design-system/tokens.css",
);
const marketingCSSPath = path.resolve(
  webRoot,
  "../marketing/assets/tokens.css",
);
const typescriptPath = path.join(
  webRoot,
  "src/shared/ui/design-system/tokens.ts",
);
const write = process.argv.includes("--write");

function flattenTokens(node, prefix = [], result = []) {
  if (!node || typeof node !== "object" || Array.isArray(node)) {
    throw new Error(`token group ${prefix.join(".")} must be an object`);
  }

  if (Object.hasOwn(node, "value")) {
    if (typeof node.value !== "string" || node.value.length === 0) {
      throw new Error(`token ${prefix.join(".")} must have a string value`);
    }
    result.push({ path: prefix.join("."), value: node.value });
    return result;
  }

  for (const [key, value] of Object.entries(node)) {
    if (key === "schemaVersion" || key === "name") continue;
    flattenTokens(value, [...prefix, key], result);
  }
  return result;
}

function cssName(tokenPath) {
  return `--vt-${tokenPath
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .replaceAll(".", "-")
    .toLowerCase()}`;
}

function tokenReference(value, knownPaths, ownerPath) {
  return value.replace(/\{([^}]+)\}/g, (_match, reference) => {
    if (!knownPaths.has(reference)) {
      throw new Error(`${ownerPath} references unknown token ${reference}`);
    }
    return `var(${cssName(reference)})`;
  });
}

function validateLayerOwnership(tokens, requiredLayers) {
  const knownPaths = new Set(tokens.map((token) => token.path));
  const layerOrder = new Map(
    requiredLayers.map((layer, index) => [layer, index]),
  );

  for (const token of tokens) {
    const [ownerLayer] = token.path.split(".");
    const references = [...token.value.matchAll(/\{([^}]+)\}/g)].map(
      (match) => match[1],
    );

    if (ownerLayer === "foundation") {
      if (references.length > 0) {
        throw new Error(
          `${token.path} is a foundation token and must own its actual value`,
        );
      }
      continue;
    }

    if (
      references.length !== 1 ||
      token.value !== `{${references[0]}}`
    ) {
      throw new Error(
        `${token.path} must be a single alias to a lower token layer`,
      );
    }

    const reference = references[0];
    if (!knownPaths.has(reference)) {
      throw new Error(`${token.path} references unknown token ${reference}`);
    }

    const [referenceLayer] = reference.split(".");
    if (
      !layerOrder.has(referenceLayer) ||
      layerOrder.get(referenceLayer) >= layerOrder.get(ownerLayer)
    ) {
      throw new Error(
        `${token.path} must reference a lower token layer, not ${reference}`,
      );
    }
  }
}

function renderCSS(source, tokens) {
  const knownPaths = new Set(tokens.map((token) => token.path));
  const lines = [
    "/* Generated from tokens.source.json. Do not edit directly. */",
    `/* Vitlane design tokens ${source.schemaVersion} · ${source.name} */`,
    ":root {",
  ];
  let currentLayer = "";

  for (const token of tokens) {
    const layer = token.path.split(".")[0];
    if (layer !== currentLayer) {
      if (currentLayer) lines.push("");
      lines.push(`  /* ${layer} */`);
      currentLayer = layer;
    }
    lines.push(
      `  ${cssName(token.path)}: ${tokenReference(token.value, knownPaths, token.path)};`,
    );
  }
  lines.push("}", "");
  return lines.join("\n");
}

function renderTypeScript(source, tokens) {
  const entries = tokens.map(
    (token) => `  ${JSON.stringify(token.path)}: "var(${cssName(token.path)})",`,
  );
  return [
    "/* Generated from tokens.source.json. Do not edit directly. */",
    `export const designTokenSchemaVersion = ${JSON.stringify(source.schemaVersion)} as const;`,
    "",
    "export const designTokens = Object.freeze({",
    ...entries,
    "} as const);",
    "",
    "export type DesignTokenName = keyof typeof designTokens;",
    "",
    "export function token(name: DesignTokenName): string {",
    "  return designTokens[name];",
    "}",
    "",
  ].join("\n");
}

async function verifyOrWrite(targetPath, expected) {
  if (write) {
    await fs.writeFile(targetPath, expected);
    return;
  }

  const actual = await fs.readFile(targetPath, "utf8").catch(() => null);
  if (actual !== expected) {
    throw new Error(
      `${path.relative(webRoot, targetPath)} is stale; run npm run design:tokens`,
    );
  }
}

const source = JSON.parse(await fs.readFile(sourcePath, "utf8"));
const requiredLayers = ["foundation", "semantic", "component", "state"];
for (const layer of requiredLayers) {
  if (!source[layer]) throw new Error(`missing token layer ${layer}`);
}

const tokens = flattenTokens(source);
validateLayerOwnership(tokens, requiredLayers);
const css = renderCSS(source, tokens);
await verifyOrWrite(cssPath, css);
await verifyOrWrite(marketingCSSPath, css);
await verifyOrWrite(typescriptPath, renderTypeScript(source, tokens));
process.stdout.write(
  `${write ? "generated" : "verified"} ${tokens.length} Vitlane design tokens\n`,
);
