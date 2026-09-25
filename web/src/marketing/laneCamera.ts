// The evidence of a Lane moment, in percent of the 1200×760 frame.
export type LaneSpot = { left: number; top: number; width: number; height: number };

// Magnification and offset (percent of the frame) applied to the captured screen.
export type LaneCamera = { zoom: number; x: number; y: number };

export const laneCameraRest: LaneCamera = { zoom: 1, x: 0, y: 0 };

const laneCameraMaxZoom = 2.4;
// Share of the frame the magnified spot may cover along its longer side.
const laneCameraFill = 88;

// On a phone the frame is about a third of its captured width. The camera
// centres the evidence and magnifies it as far as the spot still fits, never
// showing past the edge of the capture. A moment without a spot rests.
export function laneCamera(spot: LaneSpot | null): LaneCamera {
  if (!spot) return laneCameraRest;
  const zoom = Math.min(
    laneCameraMaxZoom,
    Math.max(1, Math.min(laneCameraFill / spot.width, laneCameraFill / spot.height)),
  );
  const clamp = (value: number) => Math.min(0, Math.max(100 * (1 - zoom), value));
  return {
    zoom,
    x: clamp(50 - (spot.left + spot.width / 2) * zoom),
    y: clamp(50 - (spot.top + spot.height / 2) * zoom),
  };
}

// Where the spot lands on the frame once the camera has moved.
export function laneSpotThroughCamera(spot: LaneSpot, camera: LaneCamera): LaneSpot {
  return {
    left: spot.left * camera.zoom + camera.x,
    top: spot.top * camera.zoom + camera.y,
    width: spot.width * camera.zoom,
    height: spot.height * camera.zoom,
  };
}
