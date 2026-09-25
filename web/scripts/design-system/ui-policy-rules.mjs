const rawColorPattern =
  /#[0-9a-fA-F]{3,8}\b|(?:rgb|hsl)a?\([^)]*\)/g;
const namedColorPattern =
  /\b(?:white|black|red|green|blue|gray|grey|orange|yellow|purple|pink|navy|teal|maroon|silver|lime|aqua|fuchsia)\b/gi;
const colorDeclarationPattern =
  /(?:^|[;{]\s*)((?:color|background(?:-color)?|border(?:-(?:top|right|bottom|left))?(?:-color)?|outline(?:-color)?|box-shadow|text-shadow|fill|stroke)\s*:\s*([^;{}]+))/gim;
const rawDimensionPattern = /-?(?:\d*\.)?\d+(?:px|rem|em|ms)\b/g;
const governedDimensionDeclarationPattern =
  /(?:font(?:-size)?|border-radius|(?:row-|column-)?gap|(?:margin|padding)(?:-(?:inline|inline-start|inline-end|block|block-start|block-end|top|right|bottom|left))?)\s*:[^;{}]+/gi;
const fontFamilyDeclarationPattern = /font-family\s*:[^;{}]+/gi;
const fontShorthandDeclarationPattern =
  /(?:^|[;{]\s*)font\s*:\s*([^;{}]+)/gim;
const semanticFontFamilyPattern =
  /var\(--vt-semantic-font-(?:body|data)\)/;
const customPropertyDefinitionPattern = /(--[a-zA-Z0-9_-]+)\s*:/g;
const customPropertyReferencePattern = /var\((--[a-zA-Z0-9_-]+)/g;
const moduleSpecifierPattern =
  /(?:from\s*|import\s*)["']([^"']+)["']|import\s*\(\s*["']([^"']+)["']\s*\)/g;

function count(content, pattern) {
  return content.match(pattern)?.length ?? 0;
}

export function countDirectButtons(content) {
  return count(content, /<button\b/g);
}

export function countDirectNativeControls(content) {
  return count(
    content,
    /<(?:button|input|select|textarea|details|summary)\b/g,
  );
}

export function countRawColors(content) {
  return count(content, rawColorPattern);
}

export function countRawNamedColors(content) {
  return [...content.matchAll(colorDeclarationPattern)].reduce(
    (total, match) =>
      total +
      count(
        match[2].replace(/var\([^)]*\)/g, ""),
        namedColorPattern,
      ),
    0,
  );
}

export function countRawDimensions(content) {
  return count(content, rawDimensionPattern);
}

export function countUnapprovedDesignDimensions(content) {
  return [...content.matchAll(governedDimensionDeclarationPattern)].reduce(
    (total, match) => total + count(match[0], rawDimensionPattern),
    0,
  );
}

export function countUnapprovedFontFamilies(content) {
  const longhandCount = [...content.matchAll(fontFamilyDeclarationPattern)].filter(
    ([declaration]) =>
      !/^font-family\s*:\s*var\(--vt-semantic-font-(?:body|data)\)\s*(?:!important\s*)?$/i
        .test(declaration.trim()),
  ).length;
  const shorthandCount = [...content.matchAll(fontShorthandDeclarationPattern)]
    .filter((match) => {
      const value = match[1].trim();
      return !(
        value === "inherit" ||
        semanticFontFamilyPattern.test(value) ||
        /\binherit\s*(?:!important\s*)?$/i.test(value)
      );
    }).length;
  return longhandCount + shorthandCount;
}

export function customPropertyDefinitions(content) {
  return [...content.matchAll(customPropertyDefinitionPattern)].map(
    (match) => match[1],
  );
}

export function customPropertyReferences(content) {
  return [...content.matchAll(customPropertyReferencePattern)].map(
    (match) => match[1],
  );
}

export function countLegacyControlClasses(content) {
  return count(
    content,
    /(?:className|class)\s*=\s*["'][^"']*(?:phase6-link-button|button-(?:primary|quiet|danger|small)|google-button)\b/g,
  );
}

export function hasInlineStyleObject(content) {
  return /\bstyle\s*=\s*\{\{/.test(content);
}

export function moduleSpecifiers(content) {
  return [...content.matchAll(moduleSpecifierPattern)].map(
    (match) => match[1] ?? match[2],
  );
}

export function forbiddenSharedUIImports(relativePath, content) {
  const reasons = [];
  const inSharedUI = relativePath.startsWith("src/shared/ui/");

  for (const specifier of moduleSpecifiers(content)) {
    if (
      inSharedUI &&
      (/(?:^|\/)(?:products|app)(?:\/|$)/.test(specifier) ||
        specifier.includes("../products") ||
        specifier.includes("../app"))
    ) {
      reasons.push(
        `${specifier}: shared UI cannot depend on product or app modules`,
      );
    }
    if (
      !inSharedUI &&
      /shared\/ui\//.test(specifier) &&
      !specifier.endsWith("/shared/ui")
    ) {
      reasons.push(
        `${specifier}: product code must import shared UI through its public barrel`,
      );
    }
  }

  return reasons;
}

export function directRadixImports(content) {
  return moduleSpecifiers(content).filter(
    (specifier) =>
      specifier === "radix-ui" || specifier.startsWith("@radix-ui/"),
  );
}

// ---------------------------------------------------------------------------
// Still Water Zen policy (ADR-0073). Each rule counts one kind of drift that the
// existing raw-color and dimension rules let through. The checker decides per
// rule whether a count is a report line or an error (scripts/design-system/zen-policy.json).
// ---------------------------------------------------------------------------

const foundationColorReferencePattern = /--vt-foundation-color-[a-z0-9-]+/g;
const relativeColorPattern =
  /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(\s*from\b/g;
const rawFontWeightPattern =
  /font-weight\s*:\s*(?:bold|bolder|[1-9]\d{2})\b|(?:^|[;{]\s*)font\s*:\s*(?:bold|[1-9]\d{2})\b/gim;
const uppercaseDeclarationPattern = /text-transform\s*:\s*uppercase\b/gi;
const sideBorderDeclarationPattern =
  /border-(?:inline-start|inline-end|left|right)(-width)?\s*:\s*([^;{}]+)/gi;
const buttonBorderCurrentColorPattern =
  /border(?:-color)?\s*:\s*[^;{}]*\bcurrentColor\b/gi;

function stripComments(content) {
  return content.replace(/\/\*[\s\S]*?\*\//g, "");
}

function stripNestedBlocks(body) {
  let depth = 0;
  let own = "";
  let pending = "";
  for (const character of body) {
    if (character === "{") {
      // The text since the last `;` is a nested selector, not a declaration.
      if (depth === 0) pending = "";
      depth += 1;
    } else if (character === "}") {
      depth = Math.max(0, depth - 1);
    } else if (depth === 0) {
      pending += character;
      if (character === ";") {
        own += pending;
        pending = "";
      }
    }
  }
  return own + pending;
}

// Naive CSS block walk: every `{ ... }` becomes { selector, declarations } where
// declarations exclude nested blocks. Nested rules inside @media keep their own
// selector, so a selector allowlist works regardless of at-rule nesting.
export function cssRuleBlocks(content) {
  const source = stripComments(content);
  const blocks = [];
  const open = [];
  let selectorStart = 0;
  for (let index = 0; index < source.length; index += 1) {
    const character = source[index];
    if (character === "{") {
      open.push({ selector: source.slice(selectorStart, index).trim(), bodyStart: index + 1 });
      selectorStart = index + 1;
    } else if (character === "}") {
      const block = open.pop();
      if (block) {
        blocks.push({
          selector: block.selector,
          declarations: stripNestedBlocks(source.slice(block.bodyStart, index)),
        });
      }
      selectorStart = index + 1;
    } else if (character === ";") {
      selectorStart = index + 1;
    }
  }
  return blocks;
}

// N1: product code may only consume semantic and component tokens.
export function countFoundationColorReferences(content) {
  return count(content, foundationColorReferencePattern);
}

// N2: relative color syntax computes a new color outside the token source.
export function countRelativeColorFunctions(content) {
  return count(content, relativeColorPattern);
}

// N3: uppercase belongs to the single eyebrow contract.
export function countUnapprovedUppercase(content, allowedSelectors = [".vt-eyebrow"]) {
  return cssRuleBlocks(content).reduce((total, { selector, declarations }) => {
    if (allowedSelectors.some((allowed) => selector.includes(allowed))) return total;
    return total + count(declarations, uppercaseDeclarationPattern);
  }, 0);
}

// N4: weights come from tokens, and the token scale stops at 600.
export function countRawFontWeights(content) {
  return count(stripComments(content), rawFontWeightPattern);
}

export function fontWeightTokensAbove(tokenSource, limit = 600) {
  const weights = tokenSource?.foundation?.font?.weight ?? {};
  return Object.entries(weights)
    .filter(([, token]) => Number(token?.value) > limit)
    .map(([name, token]) => `${name}=${token.value}`);
}

function isThickBorderValue(value, isWidthProperty) {
  const normalized = value.trim();
  // "0" alone is no border; "0.2rem" is a stripe.
  if (/^(?:0(?![.\d])|none|inherit|initial|unset)\b/.test(normalized)) return false;
  const tokens = [...normalized.matchAll(/var\((--[a-z0-9-]+)\)/gi)].map((match) => match[1]);
  const lengths = [...normalized.matchAll(/(-?(?:\d*\.)?\d+)(px|rem|em)\b/g)];
  for (const token of tokens) {
    if (token === "--vt-foundation-border-thin") return false;
    if (/^--vt-foundation-(?:border|space|size)-/.test(token)) return true;
  }
  for (const [, amount, unit] of lengths) {
    const number = Number(amount);
    if (unit === "px" && number > 1) return true;
    if ((unit === "rem" || unit === "em") && number > 0.07) return true;
  }
  // A width property that only names a custom property we cannot classify is
  // treated as thick: a stripe is the common reason to override one side.
  return isWidthProperty && tokens.length > 0 && lengths.length === 0
    ? !tokens.every((token) => token === "--vt-foundation-border-thin")
    : false;
}

// N5: no stripes. A side border wider than the thin token is a decoration
// unless the selector is an approved navigation position indicator.
export function countThickSideBorders(content, allowedSelectors = []) {
  return cssRuleBlocks(content).reduce((total, { selector, declarations }) => {
    if (allowedSelectors.some((allowed) => selector.includes(allowed))) return total;
    let blockTotal = 0;
    for (const match of declarations.matchAll(sideBorderDeclarationPattern)) {
      if (isThickBorderValue(match[2], Boolean(match[1]))) blockTotal += 1;
    }
    return total + blockTotal;
  }, 0);
}

// N6: buttons have no outline emphasis. currentColor borders on the button
// contract are the outline pattern the Zen system removed.
export function countOutlineButtons(content) {
  return cssRuleBlocks(content).reduce((total, { selector, declarations }) => {
    if (!selector.includes("vt-button")) return total;
    return total + count(declarations, buttonBorderCurrentColorPattern);
  }, 0);
}

// N9: radius and bold-border emphasis never sit on one element. Blocks are
// grouped into a selector family (state pseudo-classes, attribute selectors
// and `.is-*` classes stripped) so `.tab { border-radius }` plus
// `.tab[aria-pressed="true"] { border-bottom: 3px }` counts as one violation.
const radiusDeclarationPattern =
  /(?:^|[;\s])border(?:-(?:top|bottom|start|end)-(?:left|right|start|end))?-radius\s*:\s*([^;{}]+)/gi;
const boldBorderDeclarationPattern =
  /(?:^|[;\s])(border(?:-(?:top|right|bottom|left|block|inline|block-start|block-end|inline-start|inline-end))?(-width)?)\s*:\s*([^;{}]+)/gi;
const insetShadowPattern = /(?:^|[;\s])box-shadow\s*:\s*([^;{}]+)/gi;

function isNonZeroRadius(value) {
  const normalized = value.trim();
  if (/^(?:0(?![.\d])|none|inherit|initial|unset)\b/.test(normalized)) return false;
  if (/var\(--[a-z0-9-]*radius-none\)/i.test(normalized)) return false;
  if (/var\(--[a-z0-9-]*radius[a-z0-9-]*\)/i.test(normalized)) return true;
  return [...normalized.matchAll(/(-?(?:\d*\.)?\d+)(px|rem|em|%)/g)].some(
    ([, amount]) => Number(amount) > 0,
  );
}

function isBoldInsetShadow(value) {
  return value.split(",").some((layer) => {
    if (!/\binset\b/.test(layer)) return false;
    const lengths = [...layer.matchAll(/(-?(?:\d*\.)?\d+)(px|rem|em)\b/g)].map(
      ([, amount, unit]) => Math.abs(Number(amount)) * (unit === "px" ? 1 : 16),
    );
    const [x = 0, y = 0, blur = 0, spread = 0] = lengths;
    if (blur > 0) return false;
    if (/calc\(\s*var\(--vt-foundation-border-focus\)/.test(layer)) return true;
    return x >= 2 || y >= 2 || spread >= 2;
  });
}

export function selectorFamily(selector) {
  const parts = selector
    .split(",")
    .map((part) =>
      part
        .replace(/::?[a-z-]+(?:\((?:[^()]|\([^()]*\))*\))?/gi, "")
        .replace(/\[[^\]]*\]/g, "")
        .replace(/\.(?:is|has)-[a-z0-9_-]+/gi, "")
        .replace(/\s+/g, " ")
        .trim(),
    )
    .filter(Boolean);
  return [...new Set(parts)].sort().join(", ");
}

export function roundedBoldBorderFamilies(content, allowedSelectors = []) {
  const families = new Map();
  for (const { selector, declarations } of cssRuleBlocks(content)) {
    if (!selector || selector.startsWith("@")) continue;
    if (allowedSelectors.some((allowed) => selector.includes(allowed))) continue;
    const family = families.get(selectorFamily(selector)) ?? { radius: false, bold: false };
    for (const match of declarations.matchAll(radiusDeclarationPattern)) {
      if (isNonZeroRadius(match[1])) family.radius = true;
    }
    for (const match of declarations.matchAll(boldBorderDeclarationPattern)) {
      if (isThickBorderValue(match[3], Boolean(match[2]))) family.bold = true;
    }
    for (const match of declarations.matchAll(insetShadowPattern)) {
      if (isBoldInsetShadow(match[1])) family.bold = true;
    }
    families.set(selectorFamily(selector), family);
  }
  return [...families.entries()]
    .filter(([, family]) => family.radius && family.bold)
    .map(([name]) => name);
}

export function countRoundedBoldBorders(content, allowedSelectors = []) {
  return roundedBoldBorderFamilies(content, allowedSelectors).length;
}

// N10: a scrim takes light away from what sits behind a modal. `.vt-scrim`
// (components.css) is its only painter: the scrim color, dark in both themes and
// never blurred (owner 2026-09-23). A full-screen fixed layer in product CSS owns
// position and alignment only; painting it there is how a blur-only (bright in
// light) or a text-colored (bright in dark) veil came back. Blocks are grouped
// into selector families so `.x { position: fixed; inset: 0 }` plus
// `.x.is-open { background: … }` still counts.
const backgroundDeclarationPattern =
  /(?:^|[;\s])background(?:-color)?\s*:\s*([^;{}]+)/gi;
const backdropFilterDeclarationPattern =
  /(?:^|[;\s])(?:-webkit-)?backdrop-filter\s*:\s*([^;{}]+)/gi;
const fixedOverlayPattern = /(?:^|[;\s])position\s*:\s*fixed\b/i;
const fullInsetPattern =
  /(?:^|[;\s])inset\s*:\s*(?:(?:0|var\(--vt-foundation-space-0\))\s*){1,4}(?:!important\s*)?(?:;|$)/i;

function isTranslucentPaint(value) {
  return (
    /--vt-semantic-color-surface-scrim\b/.test(value) ||
    /--vt-semantic-color-text-[a-z-]+/.test(value) ||
    /color-mix\([^;{}]*\btransparent\b/.test(value)
  );
}

function isBlurValue(value) {
  return !/^(?:none|initial|unset|inherit)\s*(?:!important\s*)?$/i.test(value.trim());
}

function familyDeclarations(content, allowedSelectors) {
  const families = new Map();
  for (const { selector, declarations } of cssRuleBlocks(content)) {
    if (!selector || selector.startsWith("@")) continue;
    if (allowedSelectors.some((allowed) => selector.includes(allowed))) continue;
    const name = selectorFamily(selector);
    families.set(name, `${families.get(name) ?? ""};${declarations}`);
  }
  return families;
}

export function productScrimFamilies(content, allowedSelectors = []) {
  return [...familyDeclarations(content, allowedSelectors).entries()]
    .filter(([, declarations]) => {
      if (!fixedOverlayPattern.test(declarations)) return false;
      if (!fullInsetPattern.test(declarations)) return false;
      const paints = [...declarations.matchAll(backgroundDeclarationPattern)]
        .some((match) => isTranslucentPaint(match[1]));
      const blurs = [...declarations.matchAll(backdropFilterDeclarationPattern)]
        .some((match) => isBlurValue(match[1]));
      return paints || blurs;
    })
    .map(([name]) => name);
}

export function countProductScrims(content, allowedSelectors = []) {
  return productScrimFamilies(content, allowedSelectors).length;
}

// N11: blur never falls on a background (owner 2026-09-23). A scrim darkens what
// is behind a modal and does not blur it; the edge of a sheet, drawer or sidebar
// is a shadow. Blur is left to small local surfaces (a badge over an image, a
// popover, a button), and there it is the glass effect token — never a length
// borrowed from spacing — and what is blurred is never painted from a text
// color. In TSX the Tailwind blur utilities and fixed black/white fills bypass
// both the tokens and the theme mapping.
const glassBlurReference = "var(--vt-semantic-effect-glass-blur)";
// The landing Searching band hangs inside the reeded glass; its blur is the
// softer band token so the reeds still read through it.
const bandBlurReference = "var(--vt-semantic-effect-band-blur)";
const utilityEffectPattern =
  /(?<![\w-])(?:[\w-]+:)*(?:backdrop-(?:blur|filter)(?:-[\w-]+|-\[[^\]\s]*\]|-\([^)\s]*\))?|bg-(?:black|white)(?:\/[\w.%[\]]+)?)(?![\w-])/g;

export function unthemedEffects(content, scrimSelectors = [".vt-scrim"], bandSelectors = [".source-band"]) {
  const findings = [];
  for (const { selector, declarations } of cssRuleBlocks(content)) {
    if (!selector || selector.startsWith("@")) continue;
    const ownsScrim = scrimSelectors.some((scrim) => selector.includes(scrim));
    const ownsBand = bandSelectors.some((band) => selector.includes(band));
    const expected = ownsBand ? bandBlurReference : glassBlurReference;
    for (const match of declarations.matchAll(backdropFilterDeclarationPattern)) {
      const value = match[1].trim().replace(/\s*!important$/i, "");
      if (!isBlurValue(value)) continue;
      if (ownsScrim) findings.push(`${selector}: a scrim darkens and never blurs (backdrop-filter ${value})`);
      else if (value !== expected) findings.push(`${selector}: backdrop-filter ${value}`);
    }
  }
  for (const [name, declarations] of familyDeclarations(content, [])) {
    const blurs = [...declarations.matchAll(backdropFilterDeclarationPattern)]
      .some((match) => isBlurValue(match[1]));
    if (!blurs) continue;
    const textPainted = [...declarations.matchAll(backgroundDeclarationPattern)]
      .some((match) => /--vt-semantic-color-text-[a-z-]+/.test(match[1]));
    if (textPainted) findings.push(`${name}: blurred layer painted from a text color`);
  }
  return findings;
}

export function countUnthemedEffects(content, scrimSelectors = [".vt-scrim"], bandSelectors = [".source-band"]) {
  return unthemedEffects(content, scrimSelectors, bandSelectors).length;
}

export function countUnthemedEffectUtilities(content) {
  return count(content, utilityEffectPattern);
}
