import { execFileSync } from "node:child_process";
import { readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const requireWeb = createRequire(new URL("../../web/package.json", import.meta.url));

const numberedPhase = /phase\d+/i;
const sourceExtension = /\.(?:go|[cm]?js|tsx?|css)$/;
// Existing JSON field consumed by deployed browsers. Renaming it requires a
// versioned API migration, not an internal identifier cleanup.
const deployedField = "phase5Operator";
const goToken = /\/\/[^\n]*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|[A-Za-z_$][A-Za-z0-9_$]*/g;

export function namingViolations(path, source) {
  if (!sourceExtension.test(path)) return [];
  const violations = [];
  if (numberedPhase.test(path.split("/").at(-1))) violations.push("numbered file name");
  if (path.endsWith(".css")) {
    if (/\bphase\d+[-_]/i.test(source.replace(/\/\*[\s\S]*?\*\//g, ""))) {
      violations.push("numbered CSS selector, property or animation");
    }
    return violations;
  }
  const check = (token) => {
    if (numberedPhase.test(token) && token !== deployedField) {
      violations.push("numbered identifier: " + token);
    }
  };
  if (path.endsWith(".go")) {
    for (const [token] of source.matchAll(goToken)) {
      if (/^[A-Za-z_$]/.test(token)) check(token);
    }
  } else {
    // Parse JS/TS so regex literals, JSX and nested templates are unambiguous.
    const ts = requireWeb("typescript");
    const sourceFile = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true);
    const visit = (node) => {
      if (ts.isIdentifier(node)) check(node.text);
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }
  return [...new Set(violations)];
}

export function checkRepository(root) {
  const paths = execFileSync("git", [
    "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--",
    "server", "web", "mobile", "scripts", "tests",
  ], { cwd: root, encoding: "utf8" }).split("\0").filter(Boolean);
  const failures = [];
  for (const path of new Set(paths)) {
    if (path.startsWith("server/migrations/") || !sourceExtension.test(path)) continue;
    const fullPath = resolve(root, path);
    if (!existsSync(fullPath)) continue;
    for (const reason of namingViolations(path, readFileSync(fullPath, "utf8"))) {
      failures.push(path + ": " + reason);
    }
  }
  return failures;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const failures = checkRepository(fileURLToPath(new URL("../../", import.meta.url)));
  if (failures.length) {
    console.error(failures.join("\n"));
    process.exitCode = 1;
  } else {
    console.log("Domain naming contract passed");
  }
}
