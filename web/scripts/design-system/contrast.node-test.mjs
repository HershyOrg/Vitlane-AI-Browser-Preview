import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const source = JSON.parse(
  fs.readFileSync(
    path.resolve(
      scriptDir,
      "../../src/shared/ui/design-system/tokens.source.json",
    ),
    "utf8",
  ),
);

const pairs = [
  ["semantic.color.text.primary", "semantic.color.surface.canvas", 4.5],
  ["semantic.color.text.primary", "semantic.color.surface.base", 4.5],
  ["semantic.color.text.muted", "semantic.color.surface.canvas", 4.5],
  ["semantic.color.text.muted", "semantic.color.surface.base", 4.5],
  ["semantic.color.text.accent", "semantic.color.surface.canvas", 4.5],
  ["semantic.color.text.accent", "semantic.color.surface.base", 4.5],
  ["semantic.color.text.accent", "semantic.color.surface.selected", 4.5],
  ["semantic.color.text.danger", "semantic.color.surface.danger", 4.5],
  ["semantic.color.text.warning", "semantic.color.surface.warning", 4.5],
  ["semantic.color.text.test", "semantic.color.surface.test", 4.5],
  ["semantic.color.text.positive", "semantic.color.surface.positive", 4.5],
  ["semantic.color.text.onAction", "semantic.color.action.primary", 4.5],
  ["semantic.color.text.primary", "semantic.color.surface.selected", 4.5],
  ["semantic.color.text.comparison", "semantic.color.surface.base", 4.5],
  ["semantic.color.text.comparison", "semantic.color.surface.canvas", 4.5],
  ["semantic.color.text.comparison", "semantic.color.surface.comparison", 4.5],
  ["semantic.color.text.muted", "semantic.color.surface.comparison", 4.5],
  ["semantic.color.text.primary", "semantic.color.surface.subtle", 4.5],
  ["semantic.color.text.muted", "semantic.color.surface.subtle", 4.5],
  ["semantic.color.text.onTone", "semantic.color.border.test", 4.5],
  ["semantic.color.text.onTone", "semantic.color.border.positive", 4.5],
];

test("semantic text and surface pairs satisfy WCAG AA normal-text contrast", () => {
  for (const [foregroundPath, backgroundPath, minimum] of pairs) {
    const foreground = resolveToken(foregroundPath);
    const background = resolveToken(backgroundPath);
    const ratio = contrastRatio(foreground, background);
    assert.ok(
      ratio >= minimum,
      `${foregroundPath} ${foreground} on ${backgroundPath} ${background} has ${ratio.toFixed(3)}:1; expected at least ${minimum}:1`,
    );
  }
});

test("dark foundations stay achromatic and retain readable contrast", () => {
  const achromaticPaths = [
    "foundation.color.darkCanvas",
    "foundation.color.darkSurface",
    "foundation.color.darkSurfaceRaised",
    "foundation.color.darkBorder",
    "foundation.color.darkBorderStrong",
    "foundation.color.darkNeutralSoft",
  ];
  for (const tokenPath of achromaticPaths) {
    const color = resolveToken(tokenPath);
    const channels = rgbChannels(color);
    assert.ok(
      Math.max(...channels) - Math.min(...channels) <= 5,
      `${tokenPath} ${color} must be neutral black/gray rather than navy`,
    );
  }

  const canvas = resolveToken("foundation.color.darkCanvas");
  for (const foregroundPath of [
    "foundation.color.darkText",
    "foundation.color.darkMuted",
    "foundation.color.clear",
    "foundation.color.stillSoft",
    "foundation.color.stillBright",
  ]) {
    const foreground = resolveToken(foregroundPath);
    const ratio = contrastRatio(foreground, canvas);
    assert.ok(
      ratio >= 4.5,
      `${foregroundPath} ${foreground} on dark canvas ${canvas} has ${ratio.toFixed(3)}:1; expected at least 4.5:1`,
    );
  }
});

test("light, dark, neutral and blue theme overrides keep normal text at AA", () => {
  const combinations = [
    ["light primary / canvas", "foundation.color.night", "foundation.color.cloud"],
    ["light muted / canvas", "foundation.color.slate", "foundation.color.cloud"],
    ["light neutral action", "foundation.color.clear", "foundation.color.ink"],
    ["light still action", "foundation.color.clear", "foundation.color.still"],
    ["light still accent", "foundation.color.stillStrong", "foundation.color.clear"],
    ["light still accent / selected", "foundation.color.stillStrong", "foundation.color.stillSoft"],
    ["light still selected", "foundation.color.night", "foundation.color.stillSoft"],
    ["light still comparison / canvas", "foundation.color.stillStrong", "foundation.color.cloud"],
    ["light ink comparison / selected", "foundation.color.ink", "foundation.color.neutralSoft"],
    ["dark ink comparison / selected", "foundation.color.darkText", "foundation.color.darkNeutralSoft"],
    ["dark primary / canvas", "foundation.color.darkText", "foundation.color.darkCanvas"],
    ["dark primary / surface", "foundation.color.darkText", "foundation.color.darkSurface"],
    ["dark muted / canvas", "foundation.color.darkMuted", "foundation.color.darkCanvas"],
    ["dark neutral action", "foundation.color.darkCanvas", "foundation.color.darkText"],
    ["dark still action", "foundation.color.darkCanvas", "foundation.color.stillBright"],
    ["dark still action hover", "foundation.color.darkCanvas", "foundation.color.stillBrightHover"],
    ["dark still accent", "foundation.color.stillBright", "foundation.color.darkSurface"],
    ["dark still accent / canvas", "foundation.color.stillBright", "foundation.color.darkCanvas"],
    ["dark still accent / selected", "foundation.color.stillBright", "foundation.color.darkStillSoft"],
    ["dark still selected", "foundation.color.darkText", "foundation.color.darkStillSoft"],
    ["dark still selected muted", "foundation.color.darkMuted", "foundation.color.darkStillSoft"],
    ["dark warning", "foundation.color.warningSoft", "foundation.color.darkWarningSoft"],
  ];
  for (const [label, foregroundPath, backgroundPath] of combinations) {
    const foreground = resolveToken(foregroundPath);
    const background = resolveToken(backgroundPath);
    const ratio = contrastRatio(foreground, background);
    assert.ok(
      ratio >= 4.5,
      `${label}: ${foreground} on ${background} has ${ratio.toFixed(3)}:1; expected at least 4.5:1`,
    );
  }
});

// ADR-0082: every palette rotates the still hue and keeps its lightness, so the
// same pairs the Still Water system documents must hold for moss, maple and
// iris in both themes on the one shared canvas (a palette never paints the
// background). Ink (neutral) reuses the achromatic foundations.
test("palette families keep the Still Water contrast pairs at AA in light and dark", () => {
  const families = {
    still: ["still", "stillStrong", "stillSoft", "stillBright", "stillBrightHover", "darkStillSoft", "cloud"],
    moss: ["moss", "mossStrong", "mossSoft", "mossBright", "mossBrightHover", "darkMossSoft", "cloud"],
    maple: ["maple", "mapleStrong", "mapleSoft", "mapleBright", "mapleBrightHover", "darkMapleSoft", "cloud"],
    iris: ["iris", "irisStrong", "irisSoft", "irisBright", "irisBrightHover", "darkIrisSoft", "cloud"],
  };
  const pairs = [
    ["white text / filled action", "clear", 0],
    ["seed text / shared canvas", 0, 6],
    ["seed text / selected surface", 0, 2],
    ["strong (comparison) / surface", 1, "clear"],
    ["strong / selected surface", 1, 2],
    ["muted text / selected surface", "slate", 2],
    ["muted text / shared canvas", "slate", 6],
    ["primary text / selected surface", "night", 2],
    ["dark accent / dark canvas", 3, "darkCanvas"],
    ["dark accent / dark surface", 3, "darkSurface"],
    ["dark accent / dark selected surface", 3, 5],
    ["dark muted / dark selected surface", "darkMuted", 5],
    ["dark text / dark selected surface", "darkText", 5],
    ["dark canvas text / filled dark action", "darkCanvas", 3],
    ["dark canvas text / filled dark action hover", "darkCanvas", 4],
  ];
  for (const [family, names] of Object.entries(families)) {
    const color = (ref) => resolveToken(`foundation.color.${typeof ref === "number" ? names[ref] : ref}`);
    for (const [label, foregroundRef, backgroundRef] of pairs) {
      const foreground = color(foregroundRef);
      const background = color(backgroundRef);
      const ratio = contrastRatio(foreground, background);
      assert.ok(
        ratio >= 4.5,
        `${family} ${label}: ${foreground} on ${background} has ${ratio.toFixed(3)}:1; expected at least 4.5:1`,
      );
    }
    // The page ground is the one Still Water canvas in every palette.
    assert.equal(color(6), resolveToken("foundation.color.cloud"), `${family} must keep the shared canvas`);
  }
});

// Owner 2026-09-15: whether a modal background is blurred or translucent, it
// darkens the page in both themes. A scrim composited over every surface of its
// own theme must take away at least 40% of the luminance.
test("scrims darken the page behind a modal in both themes", () => {
  const themes = [
    ["light", "foundation.color.scrim", ["foundation.color.cloud", "foundation.color.clear", "foundation.color.neutralSoft"]],
    ["dark", "foundation.color.darkScrim", ["foundation.color.darkCanvas", "foundation.color.darkSurface", "foundation.color.darkSurfaceRaised"]],
  ];
  for (const [theme, scrimPath, surfacePaths] of themes) {
    const scrim = resolveToken(scrimPath);
    const match = scrim.match(/^rgba\(\s*(\d+),\s*(\d+),\s*(\d+),\s*(0?\.\d+)\s*\)$/);
    assert.ok(match, `${scrimPath} ${scrim} must be an rgba() scrim`);
    const [, red, green, blue, alphaText] = match;
    const alpha = Number(alphaText);
    assert.ok(alpha >= 0.4 && alpha <= 0.8, `${theme} scrim alpha ${alpha} must stay between 0.4 and 0.8`);
    for (const surfacePath of surfacePaths) {
      const surface = resolveToken(surfacePath);
      const composite = rgbChannels(surface).map((channel, index) =>
        Math.round(channel * (1 - alpha) + Number([red, green, blue][index]) * alpha),
      );
      const covered = `#${composite.map((channel) => channel.toString(16).padStart(2, "0")).join("")}`;
      const ratio = luminance(covered) / luminance(surface);
      assert.ok(
        ratio <= 0.6,
        `${theme} scrim ${scrim} over ${surfacePath} ${surface} keeps ${(ratio * 100).toFixed(1)}% of the luminance; a scrim must darken`,
      );
    }
  }
});


function oklch(hex) {
  const [red, green, blue] = rgbChannels(hex).map((channel) => {
    const value = channel / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  const l = Math.cbrt(0.4122214708 * red + 0.5363325363 * green + 0.0514459929 * blue);
  const m = Math.cbrt(0.2119034982 * red + 0.6806995451 * green + 0.1073969566 * blue);
  const s = Math.cbrt(0.0883024619 * red + 0.2817188376 * green + 0.6299787005 * blue);
  const a = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const b = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  return { C: Math.hypot(a, b), h: ((Math.atan2(b, a) * 180) / Math.PI + 360) % 360 };
}

function hueDistance(left, right) {
  const difference = Math.abs(left - right) % 360;
  return difference > 180 ? 360 - difference : difference;
}

function resolveToken(tokenPath, seen = new Set()) {
  assert.ok(!seen.has(tokenPath), `cyclic token reference: ${tokenPath}`);
  const token = tokenPath
    .split(".")
    .reduce((value, key) => value?.[key], source);
  assert.equal(typeof token?.value, "string", `missing token: ${tokenPath}`);
  const reference = token.value.match(/^\{([^}]+)\}$/)?.[1];
  if (!reference) return token.value;
  return resolveToken(reference, new Set([...seen, tokenPath]));
}

function contrastRatio(foreground, background) {
  const light = Math.max(luminance(foreground), luminance(background));
  const dark = Math.min(luminance(foreground), luminance(background));
  return (light + 0.05) / (dark + 0.05);
}

function luminance(hex) {
  assert.match(hex, /^#[0-9A-F]{6}$/i, `expected six-digit hex color: ${hex}`);
  const channels = rgbChannels(hex).map((channel) => channel / 255);
  return channels
    .map((channel) =>
      channel <= 0.04045
        ? channel / 12.92
        : ((channel + 0.055) / 1.055) ** 2.4
    )
    .reduce(
      (sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index],
      0,
    );
}

function rgbChannels(hex) {
  assert.match(hex, /^#[0-9A-F]{6}$/i, `expected six-digit hex color: ${hex}`);
  return [1, 3, 5].map((offset) =>
    Number.parseInt(hex.slice(offset, offset + 2), 16)
  );
}
