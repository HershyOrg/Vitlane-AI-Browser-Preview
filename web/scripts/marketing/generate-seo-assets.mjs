// Reproducible derivatives of existing brand and catalog assets. No network.
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { firefox } from "playwright";
import { expectedAssets, sha256 } from "../design-system/generate-brand-assets.mjs";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const manifestPath = path.join(root, "marketing/assets/seo-assets.json");
const inputs = ["shared/brand/vitlane-lane-mark.svg", "web/src/shared/ui/design-system/tokens.source.json", "marketing/assets/fonts/pretendard-semibold.woff2", "marketing/assets/fonts/pretendard-regular.woff2", "web/scripts/marketing/generate-seo-assets.mjs"];
const conversions = [];
for (const name of ["daily-trainer.svg", "knit-runner.svg", "cross-trainer.svg", "allday-walker.svg", "road-runner.svg", "cushioned-runner.svg"]) {
  conversions.push({ source: `marketing/assets/products/${name}`, target: `marketing/assets/products/optimized/${path.parse(name).name}.webp`, maxWidth: 640 });
}
inputs.push(...conversions.map(({ source }) => source));
const sourceHashes = Object.fromEntries(await Promise.all(inputs.map(async (name) => [name, sha256(await fs.readFile(path.join(root, name)))])));
if (process.argv.includes("--check")) {
  const manifest = JSON.parse(await fs.readFile(manifestPath, "utf8"));
  if (JSON.stringify(manifest.sources) !== JSON.stringify(sourceHashes)) throw new Error("SEO assets are stale; run npm run marketing:seo-assets");
  for (const [name, hash] of Object.entries(manifest.files)) {
    if (sha256(await fs.readFile(path.join(root, name))) !== hash) throw new Error(`SEO asset changed: ${name}`);
  }
  console.log("verified SEO image sources and outputs");
} else {
  const browser = await firefox.launch({ headless: true });
  const files = {};
  const save = async (name, data) => {
    await fs.mkdir(path.dirname(path.join(root, name)), { recursive: true });
    await fs.writeFile(path.join(root, name), data);
    files[name] = sha256(data);
  };
  try {
    const page = await browser.newPage({ viewport: { width: 1200, height: 630 }, deviceScaleFactor: 1 });
    for (const { source, target, maxWidth } of conversions) {
      const bytes = await fs.readFile(path.join(root, source));
      const mime = source.endsWith(".png")
        ? "image/png"
        : source.endsWith(".svg")
          ? "image/svg+xml"
          : "image/jpeg";
      const encoded = await page.evaluate(async ({ data, maxWidth }) => {
        const image = new Image(); image.src = data; await image.decode();
        const scale = Math.min(1, maxWidth / image.width);
        const canvas = document.createElement("canvas");
        canvas.width = Math.round(image.width * scale);
        canvas.height = Math.round(image.height * scale);
        const ctx = canvas.getContext("2d"); ctx.imageSmoothingQuality = "high";
        ctx.drawImage(image, 0, 0, canvas.width, canvas.height);
        return canvas.toDataURL("image/webp", 0.82);
      }, { data: `data:${mime};base64,${bytes.toString("base64")}`, maxWidth });
      if (!encoded.startsWith("data:image/webp;")) throw new Error("WebP encoder unavailable");
      await save(target, Buffer.from(encoded.split(",")[1], "base64"));
    }
    const { colors, pathData } = await expectedAssets();
    const font = async (weight) => (await fs.readFile(path.join(root, `marketing/assets/fonts/pretendard-${weight}.woff2`))).toString("base64");
    // Brand-only image shared by both locales; the HTML owns localized copy.
    await page.setContent(`<!doctype html><style>
      @font-face{font-family:Vitlane;src:url(data:font/woff2;base64,${await font("semibold")});font-weight:600}
      @font-face{font-family:Vitlane;src:url(data:font/woff2;base64,${await font("regular")});font-weight:400}
      *{box-sizing:border-box}body{margin:0;width:1200px;height:630px;overflow:hidden;background:${colors.tile};color:${colors.mark};font-family:Vitlane,sans-serif}
      .lane{position:absolute;inset:0;opacity:.08;background:repeating-linear-gradient(90deg,transparent 0 25px,${colors.mark} 26px 27px)}
      main{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;gap:38px}
      svg{width:144px;height:144px}strong{font-size:112px;font-weight:600;letter-spacing:-4px}p{position:absolute;bottom:92px;width:100%;text-align:center;margin:0;font-size:25px;letter-spacing:3px;font-weight:400}
      </style><div class="lane"></div><main><svg viewBox="0 0 32 32"><path d="${pathData}" fill="none" stroke="${colors.mark}" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/></svg><strong>Vitlane</strong></main><p>vitlane.com</p>`);
    await page.evaluate(() => document.fonts.ready);
    await save("marketing/assets/og/vitlane.png", await page.screenshot({ type: "png" }));
  } finally { await browser.close(); }
  await fs.writeFile(manifestPath, `${JSON.stringify({ sources: sourceHashes, files }, null, 2)}\n`);
  console.log(`generated ${Object.keys(files).length} SEO image assets`);
}
