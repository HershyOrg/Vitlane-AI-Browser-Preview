import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Installing is the browser's to offer (owner 2026-09-23): the App shows no install card and
// never holds back the browser's own install prompt, while it keeps everything that makes the
// App installable.
const srcRoot = fileURLToPath(new URL("..", import.meta.url));
const webRoot = fileURLToPath(new URL("../..", import.meta.url));

function productSources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return productSources(path);
    return /\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name) ? [path] : [];
  });
}

describe("browser install", () => {
  it("leaves the install prompt to the browser", () => {
    const intercepting = productSources(srcRoot).filter((path) => {
      const source = readFileSync(path, "utf8");
      return source.includes("beforeinstallprompt") || source.includes("vitlane.install-prompt");
    });
    expect(intercepting).toEqual([]);
  });

  it("keeps the App installable", () => {
    const manifest = JSON.parse(readFileSync(join(webRoot, "public/manifest.json"), "utf8")) as {
      display: string;
      start_url: string;
      icons: Array<{ sizes: string; purpose?: string }>;
    };
    expect(manifest.display).toBe("standalone");
    expect(manifest.start_url).toBe("/");
    expect(manifest.icons.map((icon) => `${icon.sizes}${icon.purpose ? ` ${icon.purpose}` : ""}`)).toEqual(
      expect.arrayContaining(["192x192", "512x512", "192x192 maskable", "512x512 maskable"]),
    );
    const head = readFileSync(join(webRoot, "index.html"), "utf8");
    expect(head).toContain('<link rel="manifest" href="/manifest.json" />');
    expect(head).toContain('<link rel="apple-touch-icon" href="/apple-touch-icon.png" />');
  });
});
