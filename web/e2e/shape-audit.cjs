// N9 (Still Water, 2026-09-14): radius and bold-border emphasis never sit on
// one rendered element. Static CSS analysis cannot see the cascade, so the
// evidence captures audit computed styles and record offenders in the manifest
// as `radiusBoldBorderViolations`. Runs inside page.evaluate — keep it
// self-contained. Focus rings are outlines, so they are not counted.
function auditRadiusBoldBorders() {
  const px = (value) => Math.abs(parseFloat(value)) || 0;
  const describe = (element, pseudo) => {
    const id = element.id ? `#${element.id}` : "";
    const names = [...element.classList];
    // Prefer product/contract class names over utility classes so the entry
    // points at a stylesheet rule, not at the primitive base class.
    const meaningful = names.filter((name) => /^(vt-|lab-|phase|curation|support|marketing|agency|order|research|account|operator)/.test(name));
    const classes = (meaningful.length > 0 ? meaningful : names)
      .slice(0, 4)
      .map((name) => `.${name}`)
      .join("");
    const text = (element.textContent || "").trim().replace(/\s+/g, " ").slice(0, 24);
    const label = element.getAttribute("aria-label") || text;
    return `${element.tagName.toLowerCase()}${id}${classes}${pseudo}${label ? ` "${label}"` : ""}`;
  };
  const boldInset = (shadow) => {
    if (!shadow || shadow === "none") return false;
    return shadow
      .replace(/(?:rgba?|hsla?|oklch|color)\([^)]*\)/g, "C")
      .split(/,\s*/)
      .some((layer) => {
        if (!/\binset\b/.test(layer)) return false;
        const lengths = (layer.match(/-?\d*\.?\d+px/g) || []).map(px);
        const [x = 0, y = 0, blur = 0, spread = 0] = lengths;
        return blur === 0 && (x >= 2 || y >= 2 || spread >= 2);
      });
  };
  const offends = (style) => {
    const rounded = [
      "borderTopLeftRadius",
      "borderTopRightRadius",
      "borderBottomRightRadius",
      "borderBottomLeftRadius",
    ].some((key) => px(style[key]) > 0);
    if (!rounded) return false;
    const boldBorder = ["Top", "Right", "Bottom", "Left"].some(
      (side) =>
        style[`border${side}Style`] !== "none" && px(style[`border${side}Width`]) >= 2,
    );
    return boldBorder || boldInset(style.boxShadow);
  };
  const violations = [];
  for (const element of document.querySelectorAll("body *")) {
    if (element.closest("svg")) continue;
    const style = getComputedStyle(element);
    if (style.display === "none" || style.visibility === "hidden") continue;
    const rect = element.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) continue;
    if (offends(style)) violations.push(describe(element, ""));
    for (const pseudo of ["::before", "::after"]) {
      const pseudoStyle = getComputedStyle(element, pseudo);
      if (pseudoStyle.content === "none" || pseudoStyle.display === "none") continue;
      if (offends(pseudoStyle)) violations.push(describe(element, pseudo));
    }
  }
  return [...new Set(violations)];
}

module.exports = { auditRadiusBoldBorders };
