// PNG and ICO icons rendered from the generated SVG tiles with Playwright
// Chromium. Run `npm run design:rasters` after the mark, the icon geometry or
// the brand colors change; `generate-brand-assets.mjs --check` verifies the
// recorded hashes without a browser.
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";
import {
  expectedAssets,
  iconSpec,
  rasterSourceHash,
  sha256,
  tileSVG,
} from "./generate-brand-assets.mjs";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(scriptDir, "../../..");
const rastersManifestPath = path.join(repositoryRoot, "shared/brand/rasters.json");

// ICO container: PNG-compressed entries, 256px is encoded as 0.
function icoFromPNGs(entries) {
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(entries.length, 4);
  const directory = Buffer.alloc(16 * entries.length);
  let offset = header.length + directory.length;
  entries.forEach(({ size, png }, index) => {
    const base = index * 16;
    directory.writeUInt8(size >= 256 ? 0 : size, base);
    directory.writeUInt8(size >= 256 ? 0 : size, base + 1);
    directory.writeUInt8(0, base + 2);
    directory.writeUInt8(0, base + 3);
    directory.writeUInt16LE(1, base + 4);
    directory.writeUInt16LE(32, base + 6);
    directory.writeUInt32LE(png.length, base + 8);
    directory.writeUInt32LE(offset, base + 12);
    offset += png.length;
  });
  return Buffer.concat([header, directory, ...entries.map(({ png }) => png)]);
}

async function render(page, svg, size) {
  const data = `data:image/svg+xml;base64,${Buffer.from(svg).toString("base64")}`;
  await page.setViewportSize({ width: size, height: size });
  await page.setContent(
    `<!doctype html><html><head><style>html,body{margin:0;background:transparent}img{display:block;width:${size}px;height:${size}px}</style></head><body><img src="${data}" alt=""></body></html>`,
  );
  await page.waitForFunction(() => document.images[0]?.complete);
  return page.screenshot({ omitBackground: true, type: "png", clip: { x: 0, y: 0, width: size, height: size } });
}

const expected = await expectedAssets();
const { pathData, colors } = expected;
const rounded = tileSVG(pathData, colors);
const bleedTouch = tileSVG(pathData, colors, { bleed: true, markScale: iconSpec.touchMarkScale });
const maskable = tileSVG(pathData, colors, { bleed: true, markScale: iconSpec.maskableMarkScale });

const browser = await chromium.launch();
const page = await browser.newPage({ deviceScaleFactor: 1 });
const outputs = new Map();
const ico = icoFromPNGs(
  await Promise.all([16, 32, 48].map(async (size) => ({ size, png: await render(page, rounded, size) }))),
);
outputs.set("web/public/favicon.ico", ico);
outputs.set("marketing/assets/favicon.ico", ico);
const touch = await render(page, bleedTouch, 180);
outputs.set("web/public/apple-touch-icon.png", touch);
outputs.set("marketing/assets/apple-touch-icon.png", touch);
outputs.set("web/public/icon-192.png", await render(page, rounded, 192));
outputs.set("web/public/icon-512.png", await render(page, rounded, 512));
outputs.set("web/public/icon-maskable-192.png", await render(page, maskable, 192));
outputs.set("web/public/icon-maskable-512.png", await render(page, maskable, 512));
await browser.close();

const files = {};
for (const [relativePath, buffer] of outputs) {
  await fs.writeFile(path.join(repositoryRoot, relativePath), buffer);
  files[relativePath] = sha256(buffer);
}
await fs.writeFile(
  rastersManifestPath,
  `${JSON.stringify({ sourceHash: rasterSourceHash(expected), generatedBy: "web/scripts/design-system/generate-brand-rasters.mjs", files }, null, 2)}\n`,
);
process.stdout.write(`rendered ${outputs.size} Vitlane brand rasters\n`);
