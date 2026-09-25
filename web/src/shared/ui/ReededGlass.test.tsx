import { readFileSync } from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReededGlass } from "./index";
import { reededGlassReedWidth } from "./reededGlassPainter";

const componentsCSS = readFileSync(
  path.join(__dirname, "design-system/components.css"),
  "utf8",
);

describe("ReededGlass contract", () => {
  it("is a hidden background layer that defaults to a still frame", () => {
    const markup = renderToStaticMarkup(<ReededGlass className="catalog-ui-login__glass" />);

    expect(markup).toContain('class="vt-reeded-glass catalog-ui-login__glass"');
    expect(markup).toContain('aria-hidden="true"');
    expect(markup).toContain('data-motion="still"');
    expect(markup).toContain('data-reed-width="22"');
    expect(markup).not.toContain("data-swell-cycle-ms");
    expect(markup).not.toContain("data-counter-swell-cycle-ms");
    expect(markup).toContain('<canvas class="vt-reeded-glass__canvas"></canvas>');
  });

  it("declares the swell tempo only when it moves", () => {
    const markup = renderToStaticMarkup(
      <ReededGlass anchor=".hero-product-link" motion="swell" />,
    );

    expect(markup).toContain('data-motion="swell"');
    expect(markup).toContain('data-swell-cycle-ms="8230"');
    expect(markup).toContain('data-counter-swell-cycle-ms="12345"');
    expect(markup).toContain('data-drift-cycle-ms="24690"');
    expect(markup).not.toContain("anchor=");
  });

  it("draws the reed rims in CSS at the same reed width the canvas uses", () => {
    expect(componentsCSS).toContain(`--vt-reeded-glass-reed: ${reededGlassReedWidth}px;`);
    expect(componentsCSS).toContain(".vt-reeded-glass::after");
    expect(componentsCSS).toContain(":root.dark .vt-reeded-glass,\n.vt-dark-scope .vt-reeded-glass");
    // The rims fade toward the copy exactly like the canvas does.
    expect(componentsCSS).toContain("--vt-reeded-glass-rim-fade");
    expect(componentsCSS).toContain('.vt-reeded-glass[data-fade="y"]');
    expect(componentsCSS).toContain("mask-image: var(--vt-reeded-glass-rim-fade);");
  });
});
