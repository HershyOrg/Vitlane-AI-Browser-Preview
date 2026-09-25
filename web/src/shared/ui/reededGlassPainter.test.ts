import { describe, expect, it } from "vitest";
import {
  paintReededGlass,
  reededGlassCounterSwellCycleMs,
  reededGlassFadeAxis,
  reededGlassReedStrength,
  reededGlassRippleCycleMs,
  reededGlassDrift,
  reededGlassDriftCycleMs,
  reededGlassLightSize,
  reededGlassLights,
  reededGlassReedWidth,
  reededGlassSlice,
  reededGlassSwellCycleMs,
  type ReededGlassPalette,
  type ReededGlassScene,
} from "./reededGlassPainter";

const scene: ReededGlassScene = {
  anchorX: 1_060,
  anchorY: 480,
  height: 900,
  pixelRatio: 2,
  width: 1_440,
};

const lightPalette: ReededGlassPalette = {
  base: "canvas",
  dark: false,
  lightBody: "body",
  lightCore: "core",
  sheen: "sheen",
};

const darkPalette: ReededGlassPalette = { ...lightPalette, dark: true, sheen: null };

describe("reeded glass (ADR-0078)", () => {
  it("keeps the still slice independent of time", () => {
    const still = reededGlassSlice(440, scene.width, 0, "still");
    expect(reededGlassSlice(440, scene.width, 12_345, "still")).toEqual(still);
    expect(still.sourceWidth).toBeCloseTo(reededGlassReedWidth * 1.6);
  });

  it("fits all three waves into the drift cycle, so the whole glass repeats as one", () => {
    expect(reededGlassCounterSwellCycleMs * 2).toBe(reededGlassSwellCycleMs * 3);
    expect(reededGlassRippleCycleMs * 2).toBe(reededGlassSwellCycleMs);
    expect(reededGlassDriftCycleMs).toBe(reededGlassSwellCycleMs * 3);
  });

  it("repeats the swell every drift cycle and keeps it within a few reeds", () => {
    const still = reededGlassSlice(440, scene.width, 0, "still");
    let largestShift = 0;
    for (let time = 0; time < reededGlassDriftCycleMs; time += 250) {
      const slice = reededGlassSlice(440, scene.width, time, "swell");
      const again = reededGlassSlice(440, scene.width, time + reededGlassDriftCycleMs, "swell");
      expect(again.sourceX).toBeCloseTo(slice.sourceX, 6);
      expect(again.sourceWidth).toBeCloseTo(slice.sourceWidth, 6);
      largestShift = Math.max(largestShift, Math.abs(slice.sourceX - still.sourceX));
    }
    expect(largestShift).toBeGreaterThan(reededGlassReedWidth * 1.5);
    expect(largestShift).toBeLessThanOrEqual(reededGlassReedWidth * 4.5);
  });

  it("lets neighboring reeds catch the light differently while it swells", () => {
    const shiftAt = (x: number) =>
      reededGlassSlice(x, scene.width, 3_000, "swell").sourceX -
      reededGlassSlice(x, scene.width, 3_000, "still").sourceX;
    const reed = reededGlassReedWidth;
    const steps = [0, 1, 2, 3, 4, 5].map((index) =>
      Math.abs(shiftAt(700 + reed * (index + 1)) - shiftAt(700 + reed * index)),
    );
    expect(Math.max(...steps)).toBeGreaterThan(reed * 0.1);
  });

  it("drifts the light a little and returns after one drift cycle", () => {
    for (let time = 0; time < reededGlassDriftCycleMs; time += 1_000) {
      const drift = reededGlassDrift(time, scene.width, scene.height);
      const again = reededGlassDrift(time + reededGlassDriftCycleMs, scene.width, scene.height);
      expect(again.x).toBeCloseTo(drift.x, 6);
      expect(again.y).toBeCloseTo(drift.y, 6);
      expect(Math.abs(drift.x)).toBeLessThanOrEqual(scene.width * 0.115);
      expect(Math.abs(drift.y)).toBeLessThanOrEqual(scene.height * 0.07);
      expect(Math.abs(drift.breath)).toBeLessThanOrEqual(0.18);
    }
  });

  it("moves the core against the body, so the light keeps its place", () => {
    const quarter = reededGlassDriftCycleMs / 4;
    const [body, core] = reededGlassLights(scene, lightPalette, quarter, "swell");
    const [stillBody, stillCore] = reededGlassLights(scene, lightPalette, quarter, "still");
    expect(body!.x - stillBody!.x).toBeGreaterThan(0);
    expect(core!.x - stillCore!.x).toBeLessThan(0);
  });

  it("keeps every moving light under full alpha, so breathing never jumps a level", () => {
    const fakeContext = (alphas: number[] = []) => {
      const context = {
        drawImage: () => {
          alphas.push(context.globalAlpha);
        },
        fillRect: () => undefined,
        fillStyle: "",
        globalAlpha: 1,
        imageSmoothingEnabled: true,
        setTransform: () => undefined,
      };
      return context;
    };
    // Only the lights are drawn with alpha; the reeds copy the result at full alpha.
    const alphas: number[] = [];
    const lightContext = fakeContext(alphas);
    const light = {
      getContext: () => lightContext,
      height: 225,
      width: 360,
    } as unknown as HTMLCanvasElement;
    for (let time = 0; time < reededGlassDriftCycleMs; time += 500) {
      paintReededGlass(
        fakeContext() as unknown as CanvasRenderingContext2D,
        { glow: () => light, light, reed: light },
        lightPalette,
        scene,
        time,
        "swell",
      );
    }
    expect(alphas.length).toBeGreaterThan(0);
    expect(Math.max(...alphas)).toBeLessThan(1);
  });

  it("gathers the light behind the anchor, with a sheen only in light surfaces", () => {
    const light = reededGlassLights(scene, lightPalette, 0, "still");
    expect(light).toHaveLength(3);
    expect(light[0]).toMatchObject({ color: "body", x: scene.anchorX });
    // Owner 2026-09-16: the lit area is a fifth smaller than the first pass
    // (0.85 → 0.76 of the height is √0.8 of the radius).
    expect(light[0]!.radius).toBeCloseTo(scene.height * 0.76);
    expect(light.every(({ strength }) => strength > 0 && strength <= 1)).toBe(true);

    const dark = reededGlassLights(scene, darkPalette, 0, "still");
    expect(dark.map(({ color }) => color)).toEqual(["body", "core"]);
  });

  it("fades the reeds out toward the copy and back in over the light", () => {
    // Owner 2026-09-16: the reeds across the headline were in the way.
    expect(reededGlassReedStrength(0)).toBe(0);
    expect(reededGlassReedStrength(0.08)).toBe(0);
    // Half way across the glass the reeds are only half there.
    expect(reededGlassReedStrength(0.42)).toBeGreaterThan(0.2);
    expect(reededGlassReedStrength(0.42)).toBeLessThan(0.8);
    expect(reededGlassReedStrength(0.76)).toBe(1);
    expect(reededGlassReedStrength(1)).toBe(1);
    let previous = -1;
    for (let step = 0; step <= 20; step += 1) {
      const strength = reededGlassReedStrength(step / 20);
      expect(strength).toBeGreaterThanOrEqual(previous);
      previous = strength;
    }
  });

  it("fades across a wide glass and up a tall one", () => {
    expect(reededGlassFadeAxis({ height: 900, width: 1_440 })).toBe("x");
    expect(reededGlassFadeAxis({ height: 844, width: 390 })).toBe("y");
  });

  it("renders the light at a quarter of the glass size", () => {
    expect(reededGlassLightSize(scene)).toEqual({ height: 225, width: 360 });
  });
});
