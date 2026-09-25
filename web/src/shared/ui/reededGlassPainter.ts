// Reeded glass (모루유리, ADR-0078): a soft Still Water light seen through
// vertical reeds. These helpers only paint; ReededGlass.tsx owns the DOM,
// token colors and the frame loop. All lengths are CSS px.

// Matches --vt-reeded-glass-reed in components.css, which draws the reed rims.
export const reededGlassReedWidth = 22;
// The light is soft, so the canvas renders at half the CSS size; the crisp
// reed rims are a static CSS layer on top and never repaint per frame.
export const reededGlassRenderScale = 0.5;
// The swell keeps the tempo the Beam settled on (8,230ms, Still Water PR F).
// A shorter wave runs against it at one and a half swells, a small ripple at
// half a swell keeps the reeds alive between them, and the light drifts at
// three swells. Every cycle divides 24,690ms, so the glass repeats as one
// without ever reading as a loop (owner, 2026-09-16: more life in it).
export const reededGlassRippleCycleMs = 4_115;
export const reededGlassSwellCycleMs = 8_230;
export const reededGlassCounterSwellCycleMs = 12_345;
export const reededGlassDriftCycleMs = 24_690;
// The light is soft, so at 15fps a frame moves it far less than a visible step.
export const reededGlassFrameIntervalMs = 66;

export type ReededGlassMotion = "still" | "swell";

// Which way the reeds fade out toward the copy: across a wide glass, up a tall
// one. The light keeps its place; only the reed texture goes (owner 2026-09-16:
// the reeds over the headline were in the way).
export type ReededGlassFade = "x" | "y";

// Each color is a computed CSS color read from semantic tokens.
export type ReededGlassPalette = {
  base: string;
  lightBody: string;
  lightCore: string;
  sheen: string | null;
  dark: boolean;
};

export type ReededGlassScene = {
  width: number;
  height: number;
  pixelRatio: number;
  anchorX: number;
  anchorY: number;
};

export type ReededGlassLight = {
  x: number;
  y: number;
  radius: number;
  strength: number;
  color: string;
};

const reedSpread = 0.6;
// How far a reed's slice moves (in reeds) and how long each wave is (a share of
// the glass width). The counter wave is shorter, the ripple shortest.
const swellWave = { shift: 2.6, wavelength: 0.34 };
const counterWave = { shift: 1.2, wavelength: 0.2 };
const rippleWave = { shift: 0.5, wavelength: 0.12 };
const spreadSwing = 0.7;
// Canvas stores alpha in 8 bits and draws alpha 1 on a different path than
// anything below it, so a light whose strength crosses 1 while it breathes
// jumps by about one level. Moving lights stay under this ceiling.
const strengthCeiling = 0.99;
// Where the reeds start and finish coming in, as a share of the fading axis.
// The faint stretch is wide on purpose: the reeds only reach full strength at
// the light (owner 2026-09-16: start fading about 30% earlier).
const reedFade = { start: 0.08, end: 0.76 };

// Smooth in, so no edge shows where the reeds begin.
export function reededGlassReedStrength(position: number) {
  const t = clamp((position - reedFade.start) / (reedFade.end - reedFade.start), 0, 1);
  return t * t * (3 - 2 * t);
}
const lightScale = 0.25;

const clamp = (value: number, min: number, max: number) =>
  Math.min(max, Math.max(min, value));

// A slow, uneven figure-eight for the light behind the glass. Only whole
// harmonics of the drift cycle, so it returns exactly after one cycle.
export function reededGlassDrift(timeMs: number, width: number, height: number) {
  const angle = (2 * Math.PI * timeMs) / reededGlassDriftCycleMs;
  return {
    x: width * (0.085 * Math.sin(angle) + 0.03 * Math.sin(angle * 3 + 0.9)),
    y: height * 0.07 * Math.sin(angle * 2 + 1.1),
    breath: 0.18 * Math.sin(angle + 0.6),
  };
}

export function reededGlassLights(
  scene: ReededGlassScene,
  palette: ReededGlassPalette,
  timeMs: number,
  motion: ReededGlassMotion,
): ReededGlassLight[] {
  const { anchorX, anchorY, height, width } = scene;
  const drift =
    motion === "swell"
      ? reededGlassDrift(timeMs, width, height)
      : { breath: 0, x: 0, y: 0 };
  const lights: ReededGlassLight[] = [
    {
      color: palette.lightBody,
      radius: height * 0.76,
      strength: (palette.dark ? 0.9 : 1) * (1 + drift.breath),
      x: anchorX + drift.x,
      y: anchorY * 0.72 + drift.y,
    },
    // The core moves against the body, so the light changes shape while its
    // center stays behind the anchor.
    {
      color: palette.lightCore,
      radius: height * 0.4,
      strength: (palette.dark ? 0.45 : 0.55) * (1 - drift.breath),
      x: anchorX + width * 0.1 - drift.x * 0.5,
      y: anchorY * 1.2 - drift.y * 0.6,
    },
  ];
  if (palette.sheen) {
    lights.push({
      color: palette.sheen,
      radius: height * 0.286,
      strength: 0.9,
      x: anchorX - width * 0.08 - drift.x * 0.4,
      y: anchorY * 0.35,
    });
  }
  return lights;
}

// Each reed shows a wider slice of the light squeezed into its width (the prism
// look). Three waves move that slice: the swell to the right, a shorter counter
// wave to the left and a small quick ripple, so neighboring reeds catch the
// light differently and the pattern between them keeps changing.
export function reededGlassSlice(
  x: number,
  width: number,
  timeMs: number,
  motion: ReededGlassMotion,
) {
  const reed = reededGlassReedWidth;
  if (motion === "still") {
    return {
      sourceWidth: reed * (1 + reedSpread),
      sourceX: x - (reed * reedSpread) / 2,
    };
  }
  const along = x / Math.max(1, width);
  const phase =
    2 * Math.PI * (along / swellWave.wavelength - timeMs / reededGlassSwellCycleMs);
  const counter =
    2 * Math.PI * (along / counterWave.wavelength + timeMs / reededGlassCounterSwellCycleMs);
  const ripple =
    2 * Math.PI * (along / rippleWave.wavelength - timeMs / reededGlassRippleCycleMs);
  const spread = reedSpread * (1 + spreadSwing * Math.sin(phase + Math.PI / 3));
  const shift =
    swellWave.shift * Math.sin(phase) +
    counterWave.shift * Math.sin(counter) +
    rippleWave.shift * Math.sin(ripple);
  return {
    sourceWidth: reed * (1 + spread),
    sourceX: x - (reed * spread) / 2 + reed * shift,
  };
}

function canvasOf(documentRef: Document, width: number, height: number) {
  const canvas = documentRef.createElement("canvas");
  canvas.width = Math.max(1, Math.round(width));
  canvas.height = Math.max(1, Math.round(height));
  return canvas;
}

// A round light with a soft edge. The color keeps its own value and only the
// alpha falls off, so no dark fringe appears where the light meets the base.
export function createReededGlassGlow(documentRef: Document, color: string) {
  const size = 128;
  const canvas = canvasOf(documentRef, size, size);
  const context = canvas.getContext("2d");
  if (!context) return canvas;
  context.fillStyle = color;
  context.fillRect(0, 0, size, size);
  const fade = context.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
  fade.addColorStop(0, color);
  fade.addColorStop(1, "transparent");
  context.globalCompositeOperation = "destination-in";
  context.fillStyle = fade;
  context.fillRect(0, 0, size, size);
  return canvas;
}

export type ReededGlassLayers = {
  light: HTMLCanvasElement;
  // The reeds are drawn here and faded before they land on the glass.
  reed: HTMLCanvasElement;
  glow: (color: string) => HTMLCanvasElement;
};

// A wide glass fades across (the copy sits on the left), a tall one fades up
// (on a phone the copy sits above the action).
export function reededGlassFadeAxis(scene: {
  width: number;
  height: number;
}): ReededGlassFade {
  return scene.width >= scene.height ? "x" : "y";
}

export function reededGlassLightSize(scene: ReededGlassScene) {
  return {
    height: Math.max(1, Math.ceil(scene.height * lightScale)),
    width: Math.max(1, Math.ceil(scene.width * lightScale)),
  };
}

export function paintReededGlass(
  context: CanvasRenderingContext2D,
  layers: ReededGlassLayers,
  palette: ReededGlassPalette,
  scene: ReededGlassScene,
  timeMs: number,
  motion: ReededGlassMotion,
) {
  const { height, pixelRatio, width } = scene;
  const light = layers.light;
  const lightContext = light.getContext("2d");
  if (!lightContext) return;
  const scale = light.width / Math.max(1, width);

  lightContext.setTransform(1, 0, 0, 1, 0, 0);
  lightContext.globalAlpha = 1;
  lightContext.fillStyle = palette.base;
  lightContext.fillRect(0, 0, light.width, light.height);
  for (const glow of reededGlassLights(scene, palette, timeMs, motion)) {
    const radius = glow.radius * scale;
    lightContext.globalAlpha = clamp(glow.strength, 0, strengthCeiling);
    lightContext.drawImage(
      layers.glow(glow.color),
      glow.x * scale - radius,
      glow.y * scale - radius,
      radius * 2,
      radius * 2,
    );
  }
  lightContext.globalAlpha = 1;

  // The glass starts as the plain light; the reeds come in over it and fade out
  // toward the copy, so the words never sit on a comb of lines.
  context.setTransform(pixelRatio, 0, 0, pixelRatio, 0, 0);
  context.globalAlpha = 1;
  context.fillStyle = palette.base;
  context.fillRect(0, 0, width, height);
  context.imageSmoothingEnabled = true;
  context.drawImage(light, 0, 0, light.width, light.height, 0, 0, width, height);

  const acrossX = reededGlassFadeAxis(scene) === "x";
  const reed = reededGlassReedWidth;
  // Fading across costs nothing: a reed is narrow enough to take one alpha.
  // Fading up needs a masked layer, and there the glass is a phone-sized canvas.
  const target = acrossX ? context : layers.reed.getContext("2d");
  if (!target) return;
  if (!acrossX) {
    target.setTransform(1, 0, 0, 1, 0, 0);
    target.globalCompositeOperation = "source-over";
    target.globalAlpha = 1;
    target.clearRect(0, 0, layers.reed.width, layers.reed.height);
    target.setTransform(pixelRatio, 0, 0, pixelRatio, 0, 0);
    target.imageSmoothingEnabled = true;
  }
  for (let x = 0; x < width; x += reed) {
    const slice = reededGlassSlice(x, width, timeMs, motion);
    const sourceX = clamp(slice.sourceX * scale, 0, light.width - 1);
    const sourceWidth = Math.max(
      1,
      Math.min(slice.sourceWidth * scale, light.width - sourceX),
    );
    if (acrossX) target.globalAlpha = reededGlassReedStrength((x + reed / 2) / width);
    target.drawImage(light, sourceX, 0, sourceWidth, light.height, x, 0, reed + 0.5, height);
  }
  target.globalAlpha = 1;
  if (acrossX) return;

  const layer = layers.reed;
  target.setTransform(1, 0, 0, 1, 0, 0);
  target.globalCompositeOperation = "destination-in";
  const fade = target.createLinearGradient(0, 0, 0, layer.height);
  // Only the alpha of these stops is read; the opaque one keeps the reeds whole.
  fade.addColorStop(0, "transparent");
  fade.addColorStop(reedFade.start, "transparent");
  fade.addColorStop(reedFade.end, palette.base);
  fade.addColorStop(1, palette.base);
  target.fillStyle = fade;
  target.fillRect(0, 0, layer.width, layer.height);
  target.globalCompositeOperation = "source-over";
  context.setTransform(1, 0, 0, 1, 0, 0);
  context.drawImage(layer, 0, 0);
}
