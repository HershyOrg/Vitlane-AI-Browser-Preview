import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import {
  countDirectNativeControls,
  countFoundationColorReferences,
  countLegacyControlClasses,
  countOutlineButtons,
  countRawColors,
  countRawFontWeights,
  countRawNamedColors,
  countRelativeColorFunctions,
  countProductScrims,
  countRoundedBoldBorders,
  countThickSideBorders,
  countUnapprovedDesignDimensions,
  countUnapprovedFontFamilies,
  countUnapprovedUppercase,
  countUnthemedEffects,
  countUnthemedEffectUtilities,
  customPropertyDefinitions,
  customPropertyReferences,
  directRadixImports,
  fontWeightTokensAbove,
  forbiddenSharedUIImports,
  hasInlineStyleObject,
} from "./ui-policy-rules.mjs";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, "../..");
const repositoryRoot = path.resolve(webRoot, "..");
const tokenSourcePath = path.join(
  webRoot,
  "src/shared/ui/design-system/tokens.source.json",
);
const tokenImpactPath = path.join(
  repositoryRoot,
  "docs/design/token-impact-map.json",
);
const componentRegistryPath = path.join(
  webRoot,
  "src/shared/ui/component-registry.json",
);
const componentsConfigPath = path.join(webRoot, "components.json");
const packagePath = path.join(webRoot, "package.json");
const copyPolicyPath = path.join(
  repositoryRoot,
  "docs/design/copy-policy.json",
);
const browserCopyPolicyPath = path.join(
  webRoot,
  "e2e/design-copy-policy.json",
);
const designLabPath = path.join(
  webRoot,
  "src/design-lab/DesignLabPage.tsx",
);
const zenPolicyPath = path.join(scriptDir, "zen-policy.json");
const generatedCSS = new Set([
  "src/shared/ui/design-system/tokens.css",
  "../marketing/assets/brand-colors.css",
  "../marketing/assets/fonts.css",
  "../marketing/assets/tokens.css",
]);
const errors = [];

async function walk(directory) {
  const entries = await fs.readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...(await walk(target)));
    else files.push(target);
  }
  return files;
}

function relativeToWeb(file) {
  return path.relative(webRoot, file).replaceAll(path.sep, "/");
}

function sha256(content) {
  return `sha256:${crypto.createHash("sha256").update(content).digest("hex")}`;
}

function sorted(values) {
  return [...values].sort((left, right) => left.localeCompare(right));
}

function sameStrings(actual, expected) {
  return JSON.stringify(sorted(actual)) === JSON.stringify(sorted(expected));
}

function exportedSharedComponents(files) {
  const names = new Set();
  const barrel = files.find(
    ({ relativePath }) => relativePath === "src/shared/ui/index.ts",
  );
  if (!barrel) return [];

  for (const match of barrel.content.matchAll(
    /export\s*{([\s\S]*?)}\s*from\s*["'][^"']+["']/g,
  )) {
    for (const entry of match[1].split(",")) {
      const candidate = entry.trim();
      if (!candidate || candidate.startsWith("type ")) continue;
      const exportedName = candidate.split(/\s+as\s+/).at(-1);
      if (/^[A-Z][A-Za-z0-9]*$/.test(exportedName)) {
        names.add(exportedName);
      }
    }
  }
  return sorted(names);
}

function fixtureNames(content) {
  return sorted(
    new Set(
      [...content.matchAll(/data-component-fixture="([^"]+)"/g)].map(
        (match) => match[1],
      ),
    ),
  );
}

const n9AuditedManifests = [
  "evidence/design-lab/manifest.json",
  "evidence/authenticated-baseline/manifest.json",
];

async function checkEvidenceManifest(relativePath, tokenSourceHash) {
  const absolutePath = path.join(repositoryRoot, relativePath);
  const manifest = JSON.parse(await fs.readFile(absolutePath, "utf8"));
  if (manifest.tokenSourceHash !== tokenSourceHash) {
    errors.push(
      `../${relativePath}: token source hash is stale; regenerate visual evidence`,
    );
  }
  // N9 runtime audit (Design Lab fixtures and authenticated routes).
  if (n9AuditedManifests.some((suffix) => relativePath.endsWith(suffix))) {
    if (!Array.isArray(manifest.radiusBoldBorderViolations)) {
      errors.push(
        `../${relativePath}: missing radiusBoldBorderViolations; regenerate visual evidence with the N9 audit`,
      );
    } else if (manifest.radiusBoldBorderViolations.length > 0) {
      errors.push(
        `../${relativePath}: N9 radius+bold-border violations (${manifest.radiusBoldBorderViolations.length})`,
      );
    }
  }
  const screenshotEntries = [
    ...(manifest.overviewResults ?? []),
    ...(manifest.results ?? []),
  ].filter((entry) => typeof entry.screenshot === "string");
  if (screenshotEntries.length === 0) {
    errors.push(`../${relativePath}: visual evidence has no screenshots`);
    return;
  }
  for (const entry of screenshotEntries) {
    const screenshot = path.join(path.dirname(absolutePath), entry.screenshot);
    try {
      const stat = await fs.stat(screenshot);
      if (!stat.isFile() || stat.size === 0) {
        errors.push(`../${relativePath}: empty screenshot ${entry.screenshot}`);
      }
    } catch {
      errors.push(`../${relativePath}: missing screenshot ${entry.screenshot}`);
    }
  }
}

const sourcePaths = [
  ...(await walk(path.join(webRoot, "src"))),
  path.join(repositoryRoot, "marketing/assets/brand-colors.css"),
  path.join(repositoryRoot, "marketing/assets/marketing.css"),
];
const sourceFiles = [];
for (const file of sourcePaths) {
  const extension = path.extname(file);
  if (![".ts", ".tsx", ".css"].includes(extension)) continue;
  const relativePath = relativeToWeb(file);
  const content = await fs.readFile(file, "utf8");
  sourceFiles.push({ content, extension, relativePath });
  const isShadcnPrimitive = relativePath.startsWith(
    "src/shared/ui/primitives/",
  );

  if (extension === ".ts" || extension === ".tsx") {
    if (
      relativePath !== "src/shared/onchain/abi.ts" &&
      countRawColors(content) > 0
    ) {
      errors.push(`${relativePath}: raw color literal is forbidden`);
    }
    if (!isShadcnPrimitive && hasInlineStyleObject(content)) {
      errors.push(`${relativePath}: inline style object is forbidden`);
    }
    if (countLegacyControlClasses(content) > 0) {
      errors.push(
        `${relativePath}: legacy control class is forbidden; use Button, ButtonLink or buttonClassName`,
      );
    }
    for (const reason of forbiddenSharedUIImports(relativePath, content)) {
      errors.push(`${relativePath}: ${reason}`);
    }
    if (!isShadcnPrimitive && directRadixImports(content).length > 0) {
      errors.push(
        `${relativePath}: Radix must be consumed through generated shadcn primitives`,
      );
    }
  }

  if (
    extension === ".tsx" &&
    !isShadcnPrimitive &&
    !relativePath.endsWith(".test.tsx") &&
    countDirectNativeControls(content) > 0
  ) {
    errors.push(
      `${relativePath}: direct native control is forbidden; use the shared shadcn layer`,
    );
  }

  if (extension === ".css" && !generatedCSS.has(relativePath)) {
    if (countRawColors(content) + countRawNamedColors(content) > 0) {
      errors.push(`${relativePath}: raw color literal is forbidden`);
    }
    if (countUnapprovedDesignDimensions(content) > 0) {
      errors.push(
        `${relativePath}: raw spacing, radius or type size is forbidden; use a design token`,
      );
    }
    if (countUnapprovedFontFamilies(content) > 0) {
      errors.push(
        `${relativePath}: font-family must use a Vitlane semantic font token`,
      );
    }
  }
}

// Still Water Zen policy (ADR-0073). Rule modes live in zen-policy.json: a
// report rule prints its current count so each migration PR can flip it to
// error once the count is 0. No count baseline is stored (ADR-0020).
const zenPolicy = JSON.parse(await fs.readFile(zenPolicyPath, "utf8"));
const zenTokenSource = JSON.parse(await fs.readFile(tokenSourcePath, "utf8"));
const zenCounters = {
  N1: ({ content }) => countFoundationColorReferences(content),
  N2: ({ content }) => countRelativeColorFunctions(content),
  N3: ({ content, extension }, rule) =>
    extension === ".css" ? countUnapprovedUppercase(content, rule.allowSelectors) : 0,
  N4: ({ content, extension }) =>
    extension === ".css" ? countRawFontWeights(content) : 0,
  N5: ({ content, extension }, rule) =>
    extension === ".css" ? countThickSideBorders(content, rule.allowSelectors) : 0,
  N6: ({ content, extension }) =>
    extension === ".css" ? countOutlineButtons(content) : 0,
  N9: ({ content, extension }, rule) =>
    extension === ".css" ? countRoundedBoldBorders(content, rule.allowSelectors) : 0,
  N10: ({ content, extension }, rule) =>
    extension === ".css" ? countProductScrims(content, rule.allowSelectors) : 0,
  N11: ({ content, extension }, rule) =>
    extension === ".css"
      ? countUnthemedEffects(content, rule.scrimSelectors, rule.bandSelectors)
      : countUnthemedEffectUtilities(content),
};
const zenReport = [];
for (const [ruleId, rule] of Object.entries(zenPolicy.rules)) {
  const counter = zenCounters[ruleId];
  if (!counter) {
    errors.push(`scripts/design-system/zen-policy.json: unknown rule ${ruleId}`);
    continue;
  }
  if (!["report", "error"].includes(rule.mode)) {
    errors.push(`scripts/design-system/zen-policy.json: ${ruleId} mode must be report or error`);
    continue;
  }
  const findings = [];
  for (const file of sourceFiles) {
    const { relativePath } = file;
    if (
      generatedCSS.has(relativePath) ||
      relativePath.endsWith(".test.ts") ||
      relativePath.endsWith(".test.tsx") ||
      (zenPolicy.exemptPaths ?? []).some((prefix) => relativePath.startsWith(prefix)) ||
      (rule.allowPaths ?? []).some((prefix) => relativePath.startsWith(prefix))
    ) {
      continue;
    }
    const total = counter(file, rule);
    if (total > 0) findings.push({ count: total, relativePath });
  }
  if (ruleId === "N4") {
    const above = fontWeightTokensAbove(zenTokenSource, rule.maxFontWeightToken ?? 600);
    if (above.length > 0) {
      findings.push({
        count: above.length,
        relativePath: `src/shared/ui/design-system/tokens.source.json (${above.join(", ")})`,
      });
    }
  }
  const total = findings.reduce((sum, finding) => sum + finding.count, 0);
  if (rule.mode === "error") {
    for (const finding of findings) {
      errors.push(`${finding.relativePath}: ${ruleId} ${rule.title} (${finding.count})`);
    }
    zenReport.push(`- ${ruleId} ${rule.title}: ${total} (error mode)`);
  } else {
    const top = findings
      .sort((left, right) => right.count - left.count)
      .slice(0, 4)
      .map((finding) => `${finding.relativePath} ${finding.count}`)
      .join(", ");
    zenReport.push(
      `- ${ruleId} ${rule.title}: ${total} in ${findings.length} files (report mode, error from ${rule.errorFrom})${top ? ` — ${top}` : ""}`,
    );
  }
}
process.stdout.write(`Still Water Zen policy report (ADR-0073)\n${zenReport.join("\n")}\n`);

const definedCustomProperties = new Set(
  sourceFiles.flatMap(({ content, extension }) =>
    extension === ".css" ? customPropertyDefinitions(content) : [],
  ),
);
const referencedCustomProperties = new Map();
for (const { content, extension, relativePath } of sourceFiles) {
  if (extension !== ".css") continue;
  for (const property of customPropertyReferences(content)) {
    const paths = referencedCustomProperties.get(property) ?? new Set();
    paths.add(relativePath);
    referencedCustomProperties.set(property, paths);
  }
}
for (const [property, paths] of referencedCustomProperties) {
  if (definedCustomProperties.has(property)) continue;
  errors.push(
    `${[...paths].sort().join(", ")}: undefined CSS custom property ${property}`,
  );
}

const componentsConfig = JSON.parse(
  await fs.readFile(componentsConfigPath, "utf8"),
);
if (
  componentsConfig.style !== "radix-nova" ||
  componentsConfig.aliases?.ui !== "@/shared/ui/primitives" ||
  componentsConfig.aliases?.utils !== "@/shared/lib/utils"
) {
  errors.push(
    "components.json: shadcn radix-nova and Vitlane primitive aliases must remain canonical",
  );
}
const packageConfig = JSON.parse(await fs.readFile(packagePath, "utf8"));
for (const dependency of [
  "class-variance-authority",
  "radix-ui",
  "tailwind-merge",
  "tw-animate-css",
]) {
  if (!packageConfig.dependencies?.[dependency]) {
    errors.push(`package.json: standard UI dependency ${dependency} is required`);
  }
}

const componentRegistry = JSON.parse(
  await fs.readFile(componentRegistryPath, "utf8"),
);
const registeredComponents = componentRegistry.components.map(
  (component) => component.name,
);
const actualComponents = exportedSharedComponents(sourceFiles);
if (!sameStrings(registeredComponents, actualComponents)) {
  errors.push(
    `src/shared/ui/component-registry.json: component coverage mismatch; expected ${actualComponents.join(", ")}`,
  );
}
for (const component of componentRegistry.components) {
  if (
    typeof component.fixture !== "string" ||
    component.fixture.length === 0 ||
    !Array.isArray(component.states) ||
    component.states.length === 0
  ) {
    errors.push(
      `src/shared/ui/component-registry.json: ${component.name} needs a fixture and states`,
    );
  }
}
const registeredFixtures = componentRegistry.components.map(
  (component) => component.fixture,
);
const designLabFixtures = fixtureNames(
  await fs.readFile(designLabPath, "utf8"),
);
if (!sameStrings(registeredFixtures, designLabFixtures)) {
  errors.push(
    `src/design-lab/DesignLabPage.tsx: component fixture coverage mismatch; expected ${sorted(new Set(registeredFixtures)).join(", ")}`,
  );
}

const copyPolicy = JSON.parse(await fs.readFile(copyPolicyPath, "utf8"));
const browserCopyPolicy = JSON.parse(
  await fs.readFile(browserCopyPolicyPath, "utf8"),
);
if (JSON.stringify(browserCopyPolicy) !== JSON.stringify(copyPolicy)) {
  errors.push(
    "e2e/design-copy-policy.json: browser copy policy is stale against docs/design/copy-policy.json",
  );
}
for (const field of [
  "consumerForbiddenTerms",
  "consumerAllowedUppercaseTerms",
  "consumerForbiddenRawEnums",
  "technicalDisclosureSelectors",
]) {
  if (!Array.isArray(copyPolicy[field]) || copyPolicy[field].length === 0) {
    errors.push(`../docs/design/copy-policy.json: ${field} must not be empty`);
  }
}

const tokenSource = await fs.readFile(tokenSourcePath);
const tokenSourceHash = sha256(tokenSource);
const tokenImpact = JSON.parse(await fs.readFile(tokenImpactPath, "utf8"));
if (tokenImpact.tokenSourceHash !== tokenSourceHash) {
  errors.push(
    "../docs/design/token-impact-map.json: token source hash is stale; update affected components and screenshots",
  );
}
if (!sameStrings(tokenImpact.components ?? [], registeredComponents)) {
  errors.push(
    "../docs/design/token-impact-map.json: components must match the shared component registry",
  );
}
if (
  !Array.isArray(tokenImpact.evidenceManifests) ||
  tokenImpact.evidenceManifests.length === 0
) {
  errors.push(
    "../docs/design/token-impact-map.json: evidenceManifests must list regenerated browser evidence",
  );
} else {
  for (const evidenceManifest of tokenImpact.evidenceManifests) {
    await checkEvidenceManifest(evidenceManifest, tokenSourceHash);
  }
}

if (errors.length > 0) {
  process.stderr.write(`${errors.map((error) => `- ${error}`).join("\n")}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write(
    "Vitlane UI policy passed: every source stylesheet, public component boundary, fixture contract and visual evidence manifest is governed\n",
  );
}
