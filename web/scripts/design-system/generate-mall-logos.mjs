import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

// Merchant badges. mall-logos.source.json maps each supported source code to a
// source-owned generic vector asset. This script checks the assets against the
// registry and renders the import map the app consumes.
//
//   node generate-mall-logos.mjs --write   re-render mall-logos.ts
//   node generate-mall-logos.mjs --check   verify files, ownership, hashes and mall-logos.ts

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, "../..");
const designSystemDir = path.join(webRoot, "src/shared/ui/design-system");
const sourcePath = path.join(designSystemDir, "mall-logos.source.json");
const logoDir = path.join(designSystemDir, "mall-logos");
const modulePath = path.join(designSystemDir, "mall-logos.ts");
const write = process.argv.includes("--write");

const codePattern = /^[A-Z][A-Z0-9_]*$/;
const filePattern = /^[a-z][a-z0-9_]*\.svg$/;

const source = JSON.parse(await fs.readFile(sourcePath, "utf8"));
const problems = [];
if (source.schemaVersion !== "vitlane.merchant-badges.v2") problems.push("schemaVersion must be vitlane.merchant-badges.v2");

const files = new Set();
for (const [code, mall] of Object.entries(source.malls ?? {})) {
  if (!codePattern.test(code)) problems.push(`${code}: mall code must be upper snake case`);
  if (!filePattern.test(mall.file ?? "")) problems.push(`${code}: file must be a lower snake case .svg`);
  files.add(mall.file);
}

for (const [file, asset] of Object.entries(source.assets ?? {})) {
  if (!filePattern.test(file)) problems.push(`${file}: asset name must be a lower snake case .svg`);
  if (asset.owner !== "HershyOrg" || asset.kind !== "source-owned-generic-vector") {
    problems.push(`${file}: asset must identify HershyOrg as the owner of a source-owned generic vector`);
  }
  const bytes = await fs.readFile(path.join(logoDir, file)).catch(() => undefined);
  if (!bytes) { problems.push(`${file}: asset is missing from mall-logos/`); continue; }
  const hash = crypto.createHash("sha256").update(bytes).digest("hex");
  if (hash !== asset.sha256) problems.push(`${file}: asset does not match its registered hash`);
  if (!bytes.toString("utf8").includes("<svg")) problems.push(`${file}: asset is not an SVG`);
}
for (const file of files) {
  if (!source.assets?.[file]) problems.push(`${file}: used by a mall but missing from assets`);
}
for (const name of await fs.readdir(logoDir)) {
  if (!source.assets?.[name]) problems.push(`mall-logos/${name} is not registered`);
}

function identifier(file) {
  return file.replace(/\.svg$/, "").replace(/_([a-z0-9])/g, (_match, letter) => letter.toUpperCase()) + "Logo";
}

const imports = [...files].sort().map((file) => `import ${identifier(file)} from "./mall-logos/${file}";`);
const entries = Object.entries(source.malls ?? {}).map(([code, mall]) => `  ${code}: ${identifier(mall.file)},`);
const rendered = [
  "/* Generated from mall-logos.source.json. Do not edit directly. */",
  ...imports,
  "",
  "/** Source-owned generic merchant badges, indexed by supported source code. */",
  "export const mallLogos: Readonly<Record<string, string>> = Object.freeze({",
  ...entries,
  "});",
  "",
].join("\n");

if (problems.length > 0) {
  for (const problem of problems) process.stderr.write(`- ${problem}\n`);
  process.exit(1);
}
if (write) {
  await fs.writeFile(modulePath, rendered);
} else {
  const current = await fs.readFile(modulePath, "utf8").catch(() => "");
  if (current !== rendered) {
    process.stderr.write(`${path.relative(webRoot, modulePath)} is stale; run npm run design:mall-logos\n`);
    process.exit(1);
  }
}
process.stdout.write(`${write ? "generated" : "verified"} ${Object.keys(source.malls).length} merchant badge mappings\n`);
