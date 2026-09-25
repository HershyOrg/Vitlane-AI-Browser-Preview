import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const themeCSS = readFileSync(new URL("./theme.css", import.meta.url), "utf8");
const shellCSS = readFileSync(new URL("../../../product-shell.css", import.meta.url), "utf8");
const componentsCSS = readFileSync(new URL("./components.css", import.meta.url), "utf8");
const tokensCSS = readFileSync(new URL("./tokens.css", import.meta.url), "utf8");
const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("product shell appearance mapping", () => {
  it("light appearance maps the sidebar to a light surface with theme-aware text", () => {
    const light = themeCSS.slice(themeCSS.indexOf(":root {"), themeCSS.indexOf(":root.dark"));
    expect(light).toContain("--vt-shell-sidebar-background: var(--vt-foundation-color-clear)");
    expect(light).toContain("--vt-shell-sidebar-text: var(--vt-foundation-color-night)");
    expect(light).toContain("--vt-shell-sidebar-border: var(--vt-foundation-color-alloy)");
    expect(light).not.toContain("--vt-shell-sidebar-background: var(--vt-foundation-color-night)");
    expect(light).not.toContain("--vt-shell-sidebar-background: var(--vt-foundation-color-carbon)");
  });

  it("still is the default accent, neutral is the alternative and dark scopes share the dark mapping", () => {
    const light = themeCSS.slice(themeCSS.indexOf(":root {"), themeCSS.indexOf(':root[data-accent="neutral"]'));
    expect(light).toContain("--primary: var(--vt-foundation-color-still)");
    expect(light).toContain("--vt-shell-sidebar-active-background: var(--vt-foundation-color-still-soft)");
    const neutral = themeCSS.slice(themeCSS.indexOf(':root[data-accent="neutral"]'), themeCSS.indexOf(':root[data-accent="moss"]'));
    expect(neutral).toContain("--vt-semantic-color-action-primary: var(--vt-foundation-color-ink)");
    // Owner 2026-09-17: Ink is black everywhere, comparison and brand included.
    expect(neutral).toContain("--vt-semantic-color-text-comparison: var(--vt-foundation-color-ink)");
    expect(neutral).toContain("--vt-semantic-color-surface-comparison: var(--vt-foundation-color-neutral-soft)");
    expect(neutral).toContain("--vt-semantic-color-action-brand: var(--vt-foundation-color-ink)");
    expect(neutral).toContain("--vt-accent-seed: var(--vt-foundation-color-ink)");
    expect(neutral).not.toContain("--vt-semantic-color-surface-canvas");
    const darkNeutral = themeCSS.slice(themeCSS.indexOf(':root.dark[data-accent="neutral"]'), themeCSS.indexOf(':root.dark[data-accent="moss"]'));
    expect(darkNeutral).toContain(':root[data-accent="neutral"] .vt-dark-scope {');
    expect(darkNeutral).toContain("--vt-semantic-color-text-comparison: var(--vt-foundation-color-dark-text)");
    expect(darkNeutral).toContain("--vt-semantic-color-action-brand: var(--vt-foundation-color-dark-text)");
    expect(componentsCSS).toContain(":root.dark .vt-appearance-controls__swatch.is-neutral {\n  background: var(--vt-foundation-color-ink);");
    expect(themeCSS).toContain(":root.dark,\n.vt-dark-scope {");
    expect(themeCSS).toContain("--vt-semantic-color-text-on-action: var(--vt-foundation-color-dark-canvas)");
    expect(themeCSS).not.toContain("route-blue");
  });

  // ADR-0082: a palette rotates one hue. Every palette block overrides the same
  // variables, in light and in dark, so no role can silently stay still in one
  // palette.
  it("moss, maple and iris override one identical set of signal variables in light and dark", () => {
    const block = (selector: string) => {
      const start = themeCSS.indexOf(selector);
      expect(start, selector).toBeGreaterThan(-1);
      const body = themeCSS.slice(themeCSS.indexOf("{", start) + 1, themeCSS.indexOf("}", start));
      return body.split("\n").map((line) => line.trim()).filter((line) => line.startsWith("--")).map((line) => line.slice(0, line.indexOf(":")));
    };
    const palettes = ["moss", "maple", "iris"];
    const light = palettes.map((id) => block(`:root[data-accent="${id}"] {`));
    const dark = palettes.map((id) => block(`:root.dark[data-accent="${id}"],`));
    for (const [index, id] of palettes.entries()) {
      expect(light[index], `light ${id}`).toEqual(light[0]);
      expect(dark[index], `dark ${id}`).toEqual(dark[0]);
      // Owner 2026-09-17: a palette never paints the background. The canvas,
      // surfaces and the shadcn muted ground stay Still Water in every palette.
      expect(light[index]).not.toContain("--vt-semantic-color-surface-canvas");
      expect(light[index]).not.toContain("--vt-semantic-color-surface-base");
      expect(light[index]).not.toContain("--muted");
      expect(light[index]).toEqual(expect.arrayContaining([
        "--vt-semantic-color-action-primary",
        "--vt-semantic-color-action-brand",
        "--vt-semantic-color-text-comparison",
        "--vt-semantic-color-surface-comparison",
        "--vt-semantic-color-border-focus",
        "--vt-accent-seed",
        "--vt-accent-strong",
      ]));
      expect(dark[index]).toEqual(expect.arrayContaining([
        "--vt-semantic-color-action-primary",
        "--vt-semantic-color-action-brand",
        "--vt-semantic-color-text-comparison",
        "--vt-component-button-primary-background",
        "--vt-state-focus-color",
      ]));
      // Dark keeps the achromatic canvas: no palette paints a dark canvas tint.
      expect(dark[index]).not.toContain("--vt-semantic-color-surface-canvas");
      // The always-dark region of a light page follows the palette too.
      expect(themeCSS).toContain(`:root.dark[data-accent="${id}"],\n:root[data-accent="${id}"] .vt-dark-scope {`);
    }
    // Light palette blocks precede the shared dark mapping (equal specificity;
    // the later dark block must win for canvas and muted).
    expect(themeCSS.indexOf(':root[data-accent="iris"] {')).toBeLessThan(themeCSS.indexOf(":root.dark,\n.vt-dark-scope {"));
    expect(themeCSS.slice(themeCSS.indexOf(":root {"), themeCSS.indexOf(':root[data-accent="neutral"]'))).toContain("--vt-accent-seed: var(--vt-foundation-color-still)");
    expect(componentsCSS).toContain("color-mix(in srgb, var(--vt-accent-seed) 40%, var(--vt-foundation-color-clear))");
    expect(componentsCSS.slice(componentsCSS.indexOf(".vt-reeded-glass {"), componentsCSS.indexOf(".vt-reeded-glass__canvas"))).not.toContain("--vt-foundation-color-still");
  });

  it("dark appearance keeps the dark sidebar and every sidebar consumer uses shell roles", () => {
    const dark = themeCSS.slice(themeCSS.indexOf(":root.dark"));
    expect(dark).toContain("--vt-shell-sidebar-background: var(--vt-foundation-color-dark-canvas)");
    expect(dark).toContain("--vt-shell-sidebar-text: var(--vt-foundation-color-clear)");
    expect(shellCSS).toContain("color: var(--vt-shell-sidebar-text)");
    expect(shellCSS).toContain("color: var(--vt-shell-sidebar-muted)");
  });

  it("sidebar controls use a restrained surface while Curations retain their prior border states", () => {
    const headerRule = shellCSS.slice(
      shellCSS.indexOf(".shell-product-sidebar__header"),
      shellCSS.indexOf(".shell-product-sidebar__home"),
    );
    const primaryStateRule = shellCSS.match(
      /\.shell-product-sidebar__primary-link:hover,[\s\S]*?\n}/,
    )?.[0] ?? "";
    const curationHoverRule = shellCSS.match(
      /\.shell-product-sidebar__list li:hover \{[\s\S]*?\n}/,
    )?.[0] ?? "";
    const curationFocusRule = shellCSS.match(
      /\.shell-product-sidebar__curation:focus-visible \{[\s\S]*?\n}/,
    )?.[0] ?? "";

    expect(shellCSS).toContain("--shell-sidebar-interaction-background");
    expect(shellCSS).toContain("var(--vt-shell-sidebar-text) 7%");
    expect(shellCSS).toContain(
      ".shell-product-sidebar__primary-link:focus-visible",
    );
    expect(primaryStateRule).toContain(
      "background: var(--shell-sidebar-interaction-background)",
    );
    expect(primaryStateRule).not.toContain("active-border");
    expect(primaryStateRule).not.toContain("active-background");
    expect(curationHoverRule).toContain("var(--vt-shell-sidebar-active-border) 48%");
    expect(curationHoverRule).not.toContain("background:");
    expect(curationFocusRule).toContain(
      "outline: var(--vt-state-focus-width) solid var(--vt-state-focus-color)",
    );
    expect(curationFocusRule).toContain(
      "outline-offset: calc(-1 * var(--vt-state-focus-width))",
    );
    expect(curationFocusRule).not.toContain("background:");
    expect(headerRule).not.toContain("border-bottom");
  });

  it("sidebar brand, icons, collapsed history and avatar follow the compact visual contract", () => {
    const currentCurationRule = shellCSS.match(
      /\.shell-product-sidebar__list li\.is-current \{[\s\S]*?\n}/,
    )?.[0] ?? "";
    expect(shellCSS).toContain("stroke-width: 1.35");
    expect(shellCSS).not.toContain(
      ".shell-product-sidebar:not(.is-collapsed)\n  .shell-product-sidebar__brand\n  .vt-brand-mark__symbol",
    );
    expect(shellCSS).not.toContain("phase6-product-sidebar__index");
    expect(shellCSS).toContain(
      ".shell-product-sidebar.is-collapsed\n  .shell-product-sidebar__history {\n  display: none;",
    );
    expect(shellCSS).toContain("border-radius: 50%");
    expect(shellCSS).toContain(
      "font-weight: var(--vt-foundation-font-weight-regular)",
    );
    expect(shellCSS).toContain(
      "font-size: var(--vt-foundation-font-size-heading-small)",
    );
    expect(shellCSS).toContain(
      ".shell-product-sidebar__brand {\n  min-width: 0;\n  min-height: var(--vt-foundation-size-touch);\n  padding-inline: var(--vt-foundation-space-3)",
    );
    expect(shellCSS).toContain(
      "border: var(--vt-foundation-border-thin) solid transparent",
    );
    expect(shellCSS).not.toContain(".shell-product-sidebar__title > small");
    expect(currentCurationRule).toContain(
      "border-inline-start-color: var(--vt-shell-sidebar-active-border)",
    );
    expect(currentCurationRule).not.toContain("background:");
  });

  it("preference recovery stays outside the product shell layout", () => {
    const preferenceAlertRule = shellCSS.match(
      /\.shell-preferences-alert \{[\s\S]*?\n}/,
    )?.[0] ?? "";
    expect(preferenceAlertRule).toContain("position: fixed");
    expect(preferenceAlertRule).toContain("z-index: var(--vt-layer-notice)");
    expect(preferenceAlertRule).not.toContain("position: static");
  });
});

// Owner 2026-09-15: a modal background darkens the page in both themes. Owner
// 2026-09-23: backgrounds are never blurred — `.vt-scrim` darkens only and the
// surface in front marks its edge with a shade. Consumers keep geometry (N10·N11).
describe("scrim contract", () => {
  it("one .vt-scrim paints the scrim color and never blurs", () => {
    const rule = componentsCSS.match(/\.vt-scrim \{[\s\S]*?\n}/)?.[0] ?? "";
    expect(rule).toContain("background: var(--vt-semantic-color-surface-scrim)");
    expect(rule).not.toContain("backdrop-filter");
    expect(tokensCSS).not.toContain("scrim-blur");
  });

  it("dark keeps its darker scrim color and deeper shades", () => {
    const dark = themeCSS.slice(themeCSS.indexOf(":root.dark"));
    expect(dark).toContain("--vt-semantic-color-surface-scrim: var(--vt-foundation-color-dark-scrim)");
    expect(dark).toContain("--vt-semantic-color-surface-recede: var(--vt-foundation-color-dark-recede)");
    for (const edge of ["start", "end", "top", "above"]) {
      expect(dark).toContain(`--vt-semantic-shadow-edge-${edge}: var(--vt-foundation-shadow-dark-edge-${edge})`);
    }
    expect(themeCSS).not.toContain("--vt-semantic-effect-");
  });

  it("shared Dialog, Sheet and Drawer overlays use the scrim, not a fixed black tint with blur", () => {
    for (const name of ["dialog", "sheet", "drawer"]) {
      const primitive = source(`../primitives/${name}.tsx`);
      const overlay = primitive.match(/data-slot="[a-z]+-overlay"[\s\S]*?cn\(\s*"([^"]+)"/)?.[1] ?? "";
      expect(overlay.split(" "), name).toContain("vt-scrim");
      expect(overlay, name).not.toMatch(/bg-black|backdrop-blur/);
    }
  });

  it("product modal, drawer, panel and mobile sidebar backdrops carry the scrim", () => {
    const backdrops: [string, string][] = [
      ["../../AppOverlay.tsx", '"shell-overlay-backdrop vt-scrim"'],
      ["../../../products/curation/iface/CatalogCurationResearch.tsx", '"catalog-ui-draft-backdrop vt-scrim"'],
      ["../../../products/curation/iface/CatalogCurationResearch.tsx", '"catalog-ui-confirm-backdrop vt-scrim"'],
      ["../../../app/App.tsx", '"shell-mobile-sidebar-backdrop vt-scrim"'],
    ];
    for (const [path, className] of backdrops) {
      expect(source(path), path).toContain(className);
    }
  });

  it("the curation sheets appear over the page without a scrim (ADR-0086)", () => {
    // A product group's list and a product's details are sheets the reader moves between while reading.
    // They neither dim nor blur the conversation; their clear layer only catches a click outside.
    for (const path of ["../../../products/curation/iface/CandidateDetailDialog.tsx", "../../../products/curation/iface/TargetSheet.tsx"]) {
      expect(source(path), path).not.toContain("vt-scrim");
    }
  });

  it("sheets, drawers and the sidebar mark their edge with a shade on the side they came from", () => {
    const sheetCSS = source("../../../products/curation/iface/curation-target-sheet.css");
    const detailCSS = source("../../../products/curation/iface/candidate-presentation.css");
    const drawerCSS = source("../../../products/curation/iface/catalog-curation-research.css");
    const dockCSS = source("../../../products/curation/iface/budget.css");
    for (const css of [sheetCSS, detailCSS]) {
      expect(css).toContain("box-shadow: var(--vt-semantic-shadow-edge-start)");
      expect(css).toContain("box-shadow: var(--vt-semantic-shadow-edge-top)");
    }
    // Two sheets deep, the one behind recedes under a thin darkening of its own, not a scrim.
    expect(sheetCSS).toContain("background: var(--vt-semantic-color-surface-recede)");
    expect(drawerCSS).toContain("box-shadow: var(--vt-semantic-shadow-edge-start)");
    expect(dockCSS).toContain("box-shadow: var(--vt-semantic-shadow-edge-above)");
    expect(shellCSS).toContain("box-shadow: var(--vt-semantic-shadow-edge-end)");
  });
});
