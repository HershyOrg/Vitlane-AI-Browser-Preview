const assert = require("node:assert/strict");

async function assertTextContrast(page, label) {
  const violations = await page.evaluate(() => {
    const root = document.documentElement;
    const rootStyle = getComputedStyle(root);
    function parseColor(value) {
      const match = value.match(
        /^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:\s*[,/]\s*([\d.]+))?\s*\)$/,
      );
      if (!match) return null;
      return {
        r: Number(match[1]),
        g: Number(match[2]),
        b: Number(match[3]),
        a: match[4] === undefined ? 1 : Number(match[4]),
      };
    }

    function solidBackground(element) {
      let current = element;
      while (current) {
        const color = parseColor(getComputedStyle(current).backgroundColor);
        if (color?.a === 1) return color;
        current = current.parentElement;
      }
      return { r: 255, g: 255, b: 255, a: 1 };
    }

    function luminance({ r, g, b }) {
      const channels = [r, g, b].map((channel) => {
        const value = channel / 255;
        return value <= 0.04045
          ? value / 12.92
          : ((value + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
    }

    function ratio(foreground, background) {
      const light = Math.max(luminance(foreground), luminance(background));
      const dark = Math.min(luminance(foreground), luminance(background));
      return (light + 0.05) / (dark + 0.05);
    }

    return [...document.querySelectorAll("body *")].flatMap((element) => {
      const ownText = [...element.childNodes]
        .filter((node) => node.nodeType === Node.TEXT_NODE)
        .map((node) => node.textContent?.trim() ?? "")
        .filter(Boolean)
        .join(" ");
      if (!ownText) return [];
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      if (
        style.display === "none" ||
        style.visibility === "hidden" ||
        Number(style.opacity) < 0.95 ||
        rect.width < 1 ||
        rect.height < 1
      ) {
        return [];
      }
      const foreground = parseColor(style.color);
      if (!foreground || foreground.a < 0.95) return [];
      const background = solidBackground(element);
      const contrast = ratio(foreground, background);
      const fontSize = Number.parseFloat(style.fontSize);
      const fontWeight = Number.parseInt(style.fontWeight, 10) || 400;
      const large = fontSize >= 24 || (fontSize >= 18.66 && fontWeight >= 700);
      const required = large ? 3 : 4.5;
      if (contrast + 0.05 >= required) return [];
      return [{
        tag: element.tagName.toLowerCase(),
        classes: typeof element.className === "string"
          ? element.className.trim()
          : "",
        scope: element
          .closest(".shell-research-group, .vt-field, article, section")
          ?.className ?? "",
        text: ownText.slice(0, 80),
        color: style.color,
        background: `rgb(${background.r}, ${background.g}, ${background.b})`,
        contrast: Number(contrast.toFixed(2)),
        required,
        root: {
          classes: root.className,
          theme: root.dataset.theme ?? "",
          accent: root.dataset.accent ?? "",
          textPrimary: rootStyle
            .getPropertyValue("--vt-semantic-color-text-primary")
            .trim(),
          textMuted: rootStyle
            .getPropertyValue("--vt-semantic-color-text-muted")
            .trim(),
          surfaceBase: rootStyle
            .getPropertyValue("--vt-semantic-color-surface-base")
            .trim(),
        },
      }];
    });
  });

  assert.deepEqual(
    violations,
    [],
    `${label} text contrast violations:\n${JSON.stringify(violations, null, 2)}`,
  );
}

module.exports = { assertTextContrast };
