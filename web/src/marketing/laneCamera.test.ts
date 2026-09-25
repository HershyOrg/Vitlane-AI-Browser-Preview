import { describe, expect, it } from "vitest";
import { laneCamera, laneCameraRest, laneSpotThroughCamera, type LaneSpot } from "./laneCamera";

// The five evidence spots of the Lane moments (main.tsx laneMoments).
const spots: LaneSpot[] = [
  { left: 26.5, top: 47.5, width: 53, height: 6 },
  { left: 26, top: 47.5, width: 33, height: 8.5 },
  { left: 27, top: 60.2, width: 61.5, height: 5.8 },
  { left: 31.5, top: 12.4, width: 10, height: 5.6 },
  { left: 25.5, top: 15.5, width: 57, height: 7.5 },
];

describe("lane camera on phones", () => {
  it("rests on the receipt moment, which has no spot", () => {
    expect(laneCamera(null)).toEqual(laneCameraRest);
  });

  it("magnifies as far as the spot fits, up to 2.4×", () => {
    expect(laneCamera(spots[3]).zoom).toBe(2.4);
    expect(laneCamera(spots[2]).zoom).toBeCloseTo(88 / 61.5);
    for (const spot of spots) {
      const { zoom } = laneCamera(spot);
      expect(zoom).toBeGreaterThanOrEqual(1);
      expect(zoom).toBeLessThanOrEqual(2.4);
    }
  });

  it("keeps every magnified spot inside the frame and never shows past the capture", () => {
    for (const spot of spots) {
      const camera = laneCamera(spot);
      expect(camera.x).toBeLessThanOrEqual(0);
      expect(camera.x).toBeGreaterThanOrEqual(100 * (1 - camera.zoom) - 1e-9);
      expect(camera.y).toBeLessThanOrEqual(0);
      expect(camera.y).toBeGreaterThanOrEqual(100 * (1 - camera.zoom) - 1e-9);
      const shown = laneSpotThroughCamera(spot, camera);
      expect(shown.left).toBeGreaterThanOrEqual(0);
      expect(shown.top).toBeGreaterThanOrEqual(0);
      expect(shown.left + shown.width).toBeLessThanOrEqual(100 + 1e-9);
      expect(shown.top + shown.height).toBeLessThanOrEqual(100 + 1e-9);
    }
  });

  it("centres the spot when the capture allows it", () => {
    const shown = laneSpotThroughCamera(spots[1], laneCamera(spots[1]));
    expect(shown.left + shown.width / 2).toBeCloseTo(50);
    expect(shown.top + shown.height / 2).toBeCloseTo(50);
  });
});
