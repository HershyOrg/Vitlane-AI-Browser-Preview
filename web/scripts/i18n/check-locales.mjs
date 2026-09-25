import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import ts from "typescript";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, "../..");
const repositoryRoot = path.resolve(webRoot, "..");
const report = process.argv.includes("--report");

const pairedMarketingDocuments = [
  "index.html",
  "privacy/index.html",
  "terms/index.html",
];

// Design Lab is a development-only fixture entry (`design-lab.html`), not a
// customer or operator route. Its production components are checked in their
// owning source files; its synthetic fixture prose is deliberately out of the
// product-copy contract.
const nonProductCopyPrefixes = ["web/src/design-lab/"];

async function walk(directory) {
  const entries = await fs.readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await walk(target));
    else files.push(target);
  }
  return files;
}

function unwrapExpression(node) {
  let current = node;
  while (ts.isAsExpression(current) || ts.isSatisfiesExpression(current) || ts.isParenthesizedExpression(current)) {
    current = current.expression;
  }
  return current;
}

function objectMessages(source, variableName) {
  let messages;
  function visit(node) {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === variableName && node.initializer) {
      const initializer = unwrapExpression(node.initializer);
      if (ts.isObjectLiteralExpression(initializer)) {
        messages = new Map();
        for (const property of initializer.properties) {
          if (!ts.isPropertyAssignment(property)) continue;
          if (!ts.isStringLiteral(property.name) && !ts.isIdentifier(property.name)) continue;
          const value = unwrapExpression(property.initializer);
          messages.set(property.name.text, ts.isStringLiteral(value) || ts.isNoSubstitutionTemplateLiteral(value)
            ? value.text
            : undefined);
        }
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(source);
  return messages;
}

function placeholders(value) {
  return [...value.matchAll(/\{([A-Za-z0-9_]+)\}/gu)].map((match) => match[1]).sort();
}

function isLocaleCallLiteral(node, name) {
  const call = node.parent;
  return ts.isCallExpression(call) && ts.isIdentifier(call.expression) && call.expression.text === name;
}

function isInvariantLiteral(node) {
  const call = node.parent;
  return ts.isCallExpression(call)
    && ts.isIdentifier(call.expression)
    && call.expression.text === "invariantContent"
    && call.arguments.length === 1
    && call.arguments[0] === node;
}

function isPairedLocaleLiteral(node) {
  const call = node.parent;
  if (!ts.isCallExpression(call) || !ts.isIdentifier(call.expression) || !["l", "ml", "localizeFixedCopy"].includes(call.expression.text)) return false;
  return call.arguments.length >= 2 && call.arguments.length <= 3 && call.arguments.slice(0, 2).includes(node);
}

const copyAttributeNames = new Set([
  "alt",
  "aria-description",
  "aria-label",
  "caption",
  "children",
  "copy",
  "description",
  "emptyMessage",
  "helperText",
  "label",
  "message",
  "placeholder",
  "subtitle",
  "title",
  "tooltip",
]);

const copyPropertyNames = new Set([
  "alt",
  "caption",
  "copy",
  "description",
  "emptyMessage",
  "helperText",
  "label",
  "message",
  "placeholder",
  "subtitle",
  "title",
  "tooltip",
]);

function propertyName(node) {
  if (ts.isIdentifier(node) || ts.isStringLiteral(node)) return node.text;
  return undefined;
}

function jsxExpressionContext(node) {
  let current = node.parent;
  while (current) {
    if (ts.isFunctionLike(current)) return undefined;
    if (ts.isCallExpression(current)) return undefined;
    if (ts.isBinaryExpression(current) && current.operatorToken.kind !== ts.SyntaxKind.PlusToken) {
      return undefined;
    }
    if (ts.isJsxAttribute(current)) return current.name.getText();
    if (ts.isJsxExpression(current)) {
      return ts.isJsxAttribute(current.parent) ? current.parent.name.getText() : "children";
    }
    current = current.parent;
  }
  return undefined;
}

function collectTypeScriptCopy(source) {
  const values = [];
  function visit(node) {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && ["l", "ml", "localizeFixedCopy"].includes(node.expression.text)) {
      const pair = node.arguments.slice(0, 2);
      const validPair = node.arguments.length >= 2 && node.arguments.length <= 3 && pair.every((argument) =>
        ts.isStringLiteral(argument) || ts.isNoSubstitutionTemplateLiteral(argument));
      if (!validPair) {
        values.push(`INVALID_LOCALE_PAIR:${node.getText()}`);
      } else {
        const [english, korean] = pair.map((argument) => argument.text);
        if (/[가-힣]/u.test(english)) values.push(`INVALID_ENGLISH_PAIR:${node.getText()}`);
        if (JSON.stringify(placeholders(english)) !== JSON.stringify(placeholders(korean))) {
          values.push(`INVALID_PAIR_PLACEHOLDERS:${node.getText()}`);
        }
      }
    }
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "invariantContent") {
      if (node.arguments.length !== 1 || !(ts.isStringLiteral(node.arguments[0]) || ts.isNoSubstitutionTemplateLiteral(node.arguments[0]))) {
        values.push(`INVALID_INVARIANT_CONTENT:${node.getText()}`);
      }
    }
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isJsxText(node)) {
      const text = node.text.trim();
      if (text && !isPairedLocaleLiteral(node) && !isLocaleCallLiteral(node, "t") && !isInvariantLiteral(node)) {
        const hangul = /[가-힣]/u.test(text);
        const hasLetter = /\p{L}/u.test(text);
        const jsxText = ts.isJsxText(node) && hasLetter;
        const directAttribute = ts.isJsxAttribute(node.parent) && copyAttributeNames.has(node.parent.name.getText());
        const context = jsxExpressionContext(node);
        const renderedExpression = context === "children" || (context && copyAttributeNames.has(context));
        const property = ts.isPropertyAssignment(node.parent)
          && copyPropertyNames.has(propertyName(node.parent.name));
        if (hasLetter && (hangul || jsxText || directAttribute || renderedExpression || property)) {
          values.push(`${node.kind}:${text}`);
        }
      }
    }
    if (ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) {
      if (/[가-힣]/u.test(node.text)) values.push(`${node.kind}:${node.text.trim()}`);
    }
    ts.forEachChild(node, visit);
  }
  visit(source);
  return values;
}

function collectHTMLCopy(content) {
  const values = [];
  for (const match of content.matchAll(/>([^<]+)</gu)) {
    const value = match[1].replace(/\s+/gu, " ").trim();
    if (/\p{L}/u.test(value)) values.push(`text:${value}`);
  }
  for (const match of content.matchAll(/\b(?:alt|aria-label|placeholder|title)=["']([^"']+)["']/gu)) {
    if (/\p{L}/u.test(match[1])) values.push(`attribute:${match[1]}`);
  }
  for (const match of content.matchAll(/<meta\s+name=["']description["']\s+content=["']([^"']+)["']/gu)) {
    if (/\p{L}/u.test(match[1])) values.push(`description:${match[1]}`);
  }
  return values;
}

function htmlTagSignature(content) {
  return [...content.matchAll(/<\/?([a-z][a-z0-9-]*)\b[^>]*>/giu)]
    .filter((match) => !match[0].startsWith("<!"))
    .map((match) => `${match[0].startsWith("</") ? "/" : ""}${match[1].toLowerCase()}`);
}

function htmlAssetSignature(content) {
  const assetContent = content.replace(
    /<link\b[^>]*\brel=["'](?:alternate|canonical)["'][^>]*>/giu,
    "",
  );
  return [...assetContent.matchAll(/\b(?:href|src)=["']([^"']+)["']/giu)]
    .map((match) => match[1])
    .filter((value) => value.startsWith("/assets/") || value.startsWith("https://"))
    .sort();
}

function hasLanguageSwitch(content, locale) {
  const target = locale === "en" ? "ko" : "en";
  return new RegExp(`<a\\b[^>]*href=["'][^"']*["'][^>]*hreflang=["']${target}["']`, "iu").test(content)
    || new RegExp(`<a\\b[^>]*hreflang=["']${target}["'][^>]*href=["'][^"']*["']`, "iu").test(content);
}

async function validateMarketingPairs() {
  const errors = [];
  for (const relative of pairedMarketingDocuments) {
    const englishPath = path.join(repositoryRoot, "marketing", relative);
    const koreanPath = path.join(repositoryRoot, "marketing/ko", relative);
    let english;
    let korean;
    try {
      [english, korean] = await Promise.all([
        fs.readFile(englishPath, "utf8"),
        fs.readFile(koreanPath, "utf8"),
      ]);
    } catch {
      errors.push(`marketing/${relative}: English/Korean document pair must both exist`);
      continue;
    }
    if (!/<html\b[^>]*\blang=["']en["']/iu.test(english)) {
      errors.push(`marketing/${relative}: English document must declare lang=\"en\"`);
    }
    if (!/<html\b[^>]*\blang=["']ko["']/iu.test(korean)) {
      errors.push(`marketing/ko/${relative}: Korean document must declare lang=\"ko\"`);
    }
    if (!hasLanguageSwitch(english, "en") || !hasLanguageSwitch(korean, "ko")) {
      errors.push(`marketing/${relative}: both documents must link to the opposite locale with hreflang`);
    }
    if (/[가-힣]/u.test(english)) {
      errors.push(`marketing/${relative}: English document contains Hangul`);
    }
    if (!/[가-힣]/u.test(korean)) {
      errors.push(`marketing/ko/${relative}: Korean document contains no Hangul copy`);
    }
    if (JSON.stringify(htmlTagSignature(english)) !== JSON.stringify(htmlTagSignature(korean))) {
      errors.push(`marketing/${relative}: English/Korean HTML structure differs`);
    }
    const englishCopyShape = collectHTMLCopy(english).map((value) => value.split(":", 1)[0]);
    const koreanCopyShape = collectHTMLCopy(korean).map((value) => value.split(":", 1)[0]);
    if (JSON.stringify(englishCopyShape) !== JSON.stringify(koreanCopyShape)) {
      errors.push(`marketing/${relative}: English/Korean text and accessible-copy slots differ`);
    }
    if (JSON.stringify(htmlAssetSignature(english)) !== JSON.stringify(htmlAssetSignature(korean))) {
      errors.push(`marketing/${relative}: English/Korean asset dependencies differ`);
    }
  }
  return errors;
}

function validateAnalyzerContract() {
  const cases = [
    ["valid pair", `const copy = l("Create curation", "새 큐레이션");`, []],
    ["valid invariant", `const protocol = invariantContent("GIWA Sepolia");`, []],
    ["raw JSX", `const view = <p>Unmapped copy</p>;`, ["Unmapped copy"]],
    ["raw Korean", `const view = <p>미매핑 문구</p>;`, ["미매핑 문구"]],
    ["Hangul in English", `const copy = l("새 큐레이션", "새 큐레이션");`, ["INVALID_ENGLISH_PAIR"]],
    ["placeholder mismatch", `const copy = l("Hello {name}", "안녕하세요");`, ["INVALID_PAIR_PLACEHOLDERS"]],
    ["dynamic pair", `const copy = l(english, korean);`, ["INVALID_LOCALE_PAIR"]],
    ["dynamic invariant", `const copy = invariantContent(value);`, ["INVALID_INVARIANT_CONTENT"]],
  ];
  const errors = [];
  for (const [name, code, expected] of cases) {
    const values = collectTypeScriptCopy(ts.createSourceFile(
      `${name}.tsx`, code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX,
    ));
    for (const marker of expected) {
      if (!values.some((value) => value.includes(marker))) {
        errors.push(`locale checker self-test failed (${name}): expected ${marker}`);
      }
    }
    if (expected.length === 0 && values.length > 0) {
      errors.push(`locale checker self-test failed (${name}): ${values.join(" | ")}`);
    }
  }
  return errors;
}

const catalogPath = path.join(webRoot, "src/shared/i18n/catalog.ts");
const catalogContent = await fs.readFile(catalogPath, "utf8");
const catalogSource = ts.createSourceFile(catalogPath, catalogContent, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
const englishCatalog = objectMessages(catalogSource, "englishMessages");
const koreanCatalog = objectMessages(catalogSource, "koreanMessages");
const catalogErrors = [];
if (!englishCatalog || !koreanCatalog) {
  catalogErrors.push("could not read English/Korean locale catalogs");
} else {
  const englishKeys = [...englishCatalog.keys()].sort();
  const koreanKeys = [...koreanCatalog.keys()].sort();
  if (JSON.stringify(englishKeys) !== JSON.stringify(koreanKeys)) {
    catalogErrors.push("English/Korean locale catalog keys do not match exactly");
  }
  for (const key of englishKeys) {
    const english = englishCatalog.get(key);
    const korean = koreanCatalog.get(key);
    if (typeof english !== "string" || typeof korean !== "string") {
      catalogErrors.push(`${key}: locale values must be deterministic string literals`);
      continue;
    }
    if (/[가-힣]/u.test(english)) catalogErrors.push(`${key}: English copy contains Hangul`);
    if (JSON.stringify(placeholders(english)) !== JSON.stringify(placeholders(korean))) {
      catalogErrors.push(`${key}: English/Korean placeholders do not match exactly`);
    }
  }
}

const candidates = (await walk(path.join(webRoot, "src")))
  .filter((file) => /\.(?:ts|tsx)$/u.test(file));

const current = {};
const currentValues = {};
for (const file of candidates) {
  const relative = path.relative(repositoryRoot, file).replaceAll(path.sep, "/");
  if (/\.(?:test|spec)\.[^.]+$/u.test(file)) continue;
  if (relative === "web/src/shared/i18n/catalog.ts") continue;
  if (nonProductCopyPrefixes.some((prefix) => relative.startsWith(prefix))) continue;
  const content = await fs.readFile(file, "utf8");
  const values = file.endsWith(".html") ? collectHTMLCopy(content) : collectTypeScriptCopy(
    ts.createSourceFile(file, content, ts.ScriptTarget.Latest, true,
      file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS),
  );
  if (values.length > 0) {
    current[relative] = { count: values.length };
    currentValues[relative] = values;
  }
}

if (report) {
  process.stdout.write(`${JSON.stringify(currentValues, null, 2)}\n`);
}

const errors = [
  ...catalogErrors,
  ...validateAnalyzerContract(),
  ...await validateMarketingPairs(),
];
if (Object.keys(current).length > 0) {
  errors.push(`production raw fixed copy must be zero; found ${Object.keys(current).length} files`);
  for (const [file, values] of Object.entries(currentValues)) {
    errors.push(`${file}: ${values.slice(0, 5).join(" | ")}${values.length > 5 ? ` | … ${values.length - 5} more` : ""}`);
  }
}

if (errors.length > 0) {
  process.stderr.write(`${errors.map((error) => `- ${error}`).join("\n")}\n`);
  process.exitCode = 1;
} else {
  process.stdout.write("Vitlane locale policy passed: all product copy is paired and marketing documents have structural parity\n");
}
