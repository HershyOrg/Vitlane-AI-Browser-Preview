// Vitlane brand assets (2026-09-14, Still Water).
//
// Source of truth: shared/brand/vitlane-lane-mark.svg (the lane mark, stroke
// in the Still Water seed). This script derives every icon surface from it:
//   - lane mark copies for the App (mask source) and Marketing
//   - favicon.svg: Still Water tile (22% radius) with the mark in clear white
//   - mask-icon.svg: mark only, black, for Safari pinned tabs
//   - manifest.json: PWA manifest with token colors
// PNG/ICO rasters come from generate-brand-rasters.mjs (Playwright) and are
// verified here by hash, so `--check` never needs a browser.
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, "../..");
const repositoryRoot = path.resolve(webRoot, "..");
const sourcePath = path.join(repositoryRoot, "shared/brand/vitlane-lane-mark.svg");
const rastersManifestPath = path.join(repositoryRoot, "shared/brand/rasters.json");
const tokenSourcePath = path.join(webRoot, "src/shared/ui/design-system/tokens.source.json");

export const laneMarkCopies = [
  "web/src/shared/ui/assets/vitlane-lane-mark.svg",
  "marketing/assets/vitlane-lane-mark.svg",
];
export const faviconCopies = ["web/public/favicon.svg", "marketing/assets/favicon.svg"];
export const maskIconCopies = ["web/public/mask-icon.svg", "marketing/assets/mask-icon.svg"];
export const manifestPath = "web/public/manifest.json";

// Icon geometry in the 32-unit mark space. `any` icons are a rounded tile,
// maskable icons bleed to the edge and keep the mark inside the 80% safe zone.
export const iconSpec = {
  tileRadius: 7,
  markScale: 0.7,
  markStrokeWidth: 3.5,
  maskableMarkScale: 0.52,
  touchMarkScale: 0.62,
};

export function laneMarkPath(source) {
  const match = /\sd="([^"]+)"/.exec(source);
  if (!match) throw new Error("shared/brand/vitlane-lane-mark.svg has no path");
  return match[1];
}

function markGroup(pathData, scale, strokeWidth, color) {
  const offset = 16 - 16 * scale;
  return (
    `<g transform="translate(${offset.toFixed(3)} ${offset.toFixed(3)}) scale(${scale})">` +
    `<path d="${pathData}" fill="none" stroke="${color}" stroke-width="${strokeWidth}" stroke-linecap="round" stroke-linejoin="round"/>` +
    `</g>`
  );
}

export function tileSVG(pathData, colors, { bleed = false, markScale = iconSpec.markScale } = {}) {
  const rx = bleed ? "" : ` rx="${iconSpec.tileRadius}"`;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">\n` +
    `  <title>Vitlane</title>\n` +
    `  <rect width="32" height="32"${rx} fill="${colors.tile}"/>\n` +
    `  ${markGroup(pathData, markScale, iconSpec.markStrokeWidth, colors.mark)}\n` +
    `</svg>\n`
  );
}

export function maskIconSVG(pathData) {
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">\n` +
    `  <title>Vitlane</title>\n` +
    `  ${markGroup(pathData, 1, 3, "#000000")}\n` +
    `</svg>\n`
  );
}

export function manifestJSON(colors) {
  return `${JSON.stringify(
    {
      id: "/",
      name: "Vitlane",
      short_name: "Vitlane",
      description: "Vitlane turns a purchase request into researched candidates and a verified order.",
      lang: "en",
      start_url: "/",
      scope: "/",
      display: "standalone",
      background_color: colors.canvas,
      theme_color: colors.canvas,
      categories: ["shopping"],
      icons: [
        { src: "/icon-192.png", sizes: "192x192", type: "image/png" },
        { src: "/icon-512.png", sizes: "512x512", type: "image/png" },
        { src: "/icon-maskable-192.png", sizes: "192x192", type: "image/png", purpose: "maskable" },
        { src: "/icon-maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
      ],
    },
    null,
    2,
  )}\n`;
}

export async function brandColors() {
  const tokens = JSON.parse(await fs.readFile(tokenSourcePath, "utf8"));
  const color = tokens.foundation.color;
  return { tile: color.still.value, mark: color.clear.value, canvas: color.cloud.value, dark: color.darkCanvas.value };
}

export function sha256(content) {
  return `sha256:${crypto.createHash("sha256").update(content).digest("hex")}`;
}

export async function expectedAssets() {
  const source = await fs.readFile(sourcePath, "utf8");
  const colors = await brandColors();
  const pathData = laneMarkPath(source);
  const files = new Map();
  for (const copy of laneMarkCopies) files.set(copy, source);
  const favicon = tileSVG(pathData, colors);
  for (const copy of faviconCopies) files.set(copy, favicon);
  const maskIcon = maskIconSVG(pathData);
  for (const copy of maskIconCopies) files.set(copy, maskIcon);
  files.set(manifestPath, manifestJSON(colors));
  return { source, colors, pathData, files };
}

// Rasters are stale when the mark, the icon geometry or the colors change.
export function rasterSourceHash({ source, colors }) {
  return sha256(JSON.stringify({ source, colors, iconSpec }));
}

async function main() {
  const write = process.argv.includes("--write");
  const expected = await expectedAssets();
  if (write) {
    for (const [relativePath, content] of expected.files) {
      const target = path.join(repositoryRoot, relativePath);
      await fs.mkdir(path.dirname(target), { recursive: true });
      await fs.writeFile(target, content);
    }
    process.stdout.write("generated Vitlane brand assets\n");
    return;
  }
  for (const [relativePath, content] of expected.files) {
    const actual = await fs.readFile(path.join(repositoryRoot, relativePath), "utf8").catch(() => null);
    if (actual !== content) {
      throw new Error(`${relativePath} is stale; run npm run design:assets`);
    }
  }
  const rasters = JSON.parse(await fs.readFile(rastersManifestPath, "utf8").catch(() => "null"));
  if (!rasters || rasters.sourceHash !== rasterSourceHash(expected)) {
    throw new Error("shared/brand/rasters.json is stale; run npm run design:rasters");
  }
  for (const [relativePath, hash] of Object.entries(rasters.files)) {
    const actual = await fs.readFile(path.join(repositoryRoot, relativePath)).catch(() => null);
    if (!actual || sha256(actual) !== hash) {
      throw new Error(`${relativePath} does not match rasters.json; run npm run design:rasters`);
    }
  }
  process.stdout.write("verified Vitlane brand assets\n");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main();
}
