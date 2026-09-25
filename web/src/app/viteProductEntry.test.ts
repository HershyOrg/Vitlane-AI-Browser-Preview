import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// Release packaging accepts exactly one `/assets/index-*.js` bundle. Rollup
// names the bundle after the input key, so the product entry key stays `index`.
describe("vite product entry", () => {
  it("keeps the product entry keyed as index so the release bundle is assets/index-*.js", () => {
    const source = readFileSync(path.resolve(__dirname, "../../vite.config.ts"), "utf8");
    const input = /rollupOptions:\s*\{[\s\S]*?input:\s*\{([\s\S]*?)\}/.exec(source)?.[1] ?? "";
    expect(input).toMatch(/\bindex:\s*fileURLToPath\(new URL\("\.\/index\.html"/);
    expect(input).not.toMatch(/\bmain:/);
  });
});
