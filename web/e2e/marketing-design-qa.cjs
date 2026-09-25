const { firefox } = require("playwright");
const fs = require("node:fs");
const path = require("node:path");

const sourceRoot = process.env.MARKETING_REFERENCE_DIR;
if (!sourceRoot) {
  throw new Error("MARKETING_REFERENCE_DIR must point to the local reference images");
}
const evidenceDir = process.env.MARKETING_EVIDENCE_DIR ||
  path.resolve(__dirname, "../../docs/design/evidence/marketing-living-lane");
const baseURL = process.env.MARKETING_BASE_URL || "http://127.0.0.1:4173";
const output = path.join(evidenceDir, "07-reference-comparison.png");
const curveImplementation = path.join(evidenceDir, "08-curve-reference-viewport.png");

function dataURL(file) {
  return `data:image/png;base64,${fs.readFileSync(file).toString("base64")}`;
}

const comparisons = [
  ["S-curve Beam and line shadows", "ChatGPT Image 2026년 8월 12일 오후 10_10_55.png", "08-curve-reference-viewport.png"],
  ["Hero typography and inline brand", "첫 화면 문구 1.png", "01-hero-desktop.png"],
  ["Payment status lane", "결제 처리 절차 1.png", "03-payment-desktop.png"],
  ["Printed receipt", "영수증 1.png", "04-receipt-desktop.png"],
  ["Scroll-tilted product cards", "하단의 기울어지는 제품 이미지 1.png", "05-gallery-desktop.png"],
];

(async () => {
  const browser = await firefox.launch({ headless: true });
  const implementationPage = await browser.newPage({
    viewport: { width: 1536, height: 1024 },
    deviceScaleFactor: 1,
  });
  await implementationPage.emulateMedia({ reducedMotion: "reduce" });
  await implementationPage.goto(`${baseURL}/`, { waitUntil: "networkidle" });
  await implementationPage.locator("#hero-title").waitFor();
  await implementationPage.waitForFunction(() =>
    [...document.images].every((image) => image.complete),
  );
  await implementationPage.screenshot({ path: curveImplementation, fullPage: false });
  await implementationPage.close();

  const page = await browser.newPage({ viewport: { width: 1880, height: 1200 } });
  const rows = comparisons.map(([label, source, implementation]) => `
    <section>
      <h2>${label}</h2>
      <div class="pair">
        <figure><figcaption>REFERENCE</figcaption><img src="${dataURL(path.join(sourceRoot, source))}" /></figure>
        <figure><figcaption>IMPLEMENTATION</figcaption><img src="${dataURL(path.join(evidenceDir, implementation))}" /></figure>
      </div>
    </section>
  `).join("");
  await page.setContent(`<!doctype html><html lang="ko"><head><meta charset="UTF-8" /><style>
    *{box-sizing:border-box}body{margin:0;padding:40px;background:#101114;color:#f3f4f6;font-family:Arial,sans-serif}
    h1{margin:0 0 40px;font-size:28px}section{padding:28px 0 44px;border-top:1px solid #303235}
    h2{margin:0 0 18px;font-size:20px}.pair{display:grid;grid-template-columns:1fr 1fr;gap:24px;align-items:start}
    figure{margin:0;padding:14px;background:#1d1f22;border:1px solid #303235}figcaption{margin-bottom:12px;color:#a7aab1;font:12px monospace;letter-spacing:.08em}
    img{display:block;width:100%;height:620px;object-fit:contain;background:#16171a}
  </style></head><body><h1>Vitlane Beam · source / implementation comparison</h1>${rows}</body></html>`);
  await page.waitForFunction(() => [...document.images].every((image) => image.complete));
  await page.screenshot({ path: output, fullPage: true });
  await browser.close();
  console.log(output);
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
