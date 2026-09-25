import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(scriptDirectory, "../../..");
const sourcePath = path.join(repositoryRoot, "marketing/brand-colors.json");
const targetPath = path.join(repositoryRoot, "marketing/assets/brand-colors.css");
const write = process.argv.includes("--write");
const palette = JSON.parse(await fs.readFile(sourcePath, "utf8"));
const requiredBrands = [
  "usd",
  "bitcoin",
  "ethereum",
  "usdc",
  "paypal",
  "shopify",
  "amazon",
  "airbnb",
  "naver",
];

for (const brand of requiredBrands) {
  if (!/^#[0-9A-F]{6}$/.test(palette[brand] ?? "")) {
    throw new Error(`marketing brand color is missing or invalid: ${brand}`);
  }
}

const css = [
  "/* Generated from marketing/brand-colors.json. Do not edit directly. */",
  ":root {",
  ...requiredBrands.map(
    (brand) => `  --vt-marketing-brand-${brand}: ${palette[brand]};`,
  ),
  "}",
  "",
].join("\n");

if (write) {
  await fs.writeFile(targetPath, css);
  console.log(`generated ${requiredBrands.length} Marketing brand colors`);
} else {
  const current = await fs.readFile(targetPath, "utf8").catch(() => "");
  if (current !== css) {
    throw new Error("marketing/assets/brand-colors.css is stale; run npm run marketing:brand-colors");
  }
  console.log(`verified ${requiredBrands.length} Marketing brand colors`);
}
