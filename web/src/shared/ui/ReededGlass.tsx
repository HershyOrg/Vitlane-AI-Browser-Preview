import { useEffect, useRef } from "react";
import { cn } from "../lib/utils";
import "./design-system/components.css";
import {
  createReededGlassGlow,
  paintReededGlass,
  reededGlassFadeAxis,
  reededGlassCounterSwellCycleMs,
  reededGlassDriftCycleMs,
  reededGlassFrameIntervalMs,
  reededGlassLightSize,
  reededGlassReedWidth,
  reededGlassRenderScale,
  reededGlassSwellCycleMs,
  type ReededGlassMotion,
  type ReededGlassPalette,
  type ReededGlassScene,
} from "./reededGlassPainter";

export type { ReededGlassMotion } from "./reededGlassPainter";

// ADR-0078 (component-contracts 13): the Still Water background surface. It
// fills its positioned parent, paints its base from its own background token
// and draws the light from the component variables in components.css. `swell`
// moves the light and the reeds slowly; reduced motion, a hidden tab or an
// offscreen glass show a still frame. Without a 2D canvas the token background
// stays.
export interface ReededGlassProps {
  motion?: ReededGlassMotion;
  // Selector of the element the light gathers behind, e.g. the hero action.
  anchor?: string;
  className?: string;
}

const defaultAnchor = { x: 0.72, y: 0.3 };

// A computed color with zero alpha: the token was not resolved (yet).
function isClear(value: string) {
  const channels = value.match(/[\d.]+/g);
  return !channels || (channels.length >= 4 && Number(channels.at(-1)) === 0);
}

// Colors come from the component variables in components.css; the probe
// resolves them to computed colors a 2D canvas accepts. Returns null until the
// stylesheet that defines them has applied.
function readPalette(root: HTMLElement): ReededGlassPalette | null {
  const dark =
    Boolean(root.closest(".vt-dark-scope")) ||
    document.documentElement.classList.contains("dark");
  const probe = document.createElement("span");
  probe.style.position = "absolute";
  // Reduced motion shortens every transition to 0.01ms with !important
  // (styles.css, theme.css); a transitioning probe would report the previous
  // color, so it must not transition at all.
  probe.style.setProperty("transition", "none", "important");
  root.append(probe);
  const read = (name: string) => {
    probe.style.color = `var(${name}, transparent)`;
    return window.getComputedStyle(probe).color;
  };
  const palette: ReededGlassPalette = {
    base: window.getComputedStyle(root).backgroundColor,
    dark,
    lightBody: read("--vt-reeded-glass-light-body"),
    lightCore: read("--vt-reeded-glass-light-core"),
    sheen: dark ? null : read("--vt-reeded-glass-sheen"),
  };
  probe.remove();
  return isClear(palette.base) || isClear(palette.lightBody) ? null : palette;
}

export function ReededGlass({
  motion = "still",
  anchor,
  className,
}: ReededGlassProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const root = rootRef.current;
    const canvas = canvasRef.current;
    if (!root || !canvas) return;
    const context = canvas.getContext("2d");
    if (!context) return;

    const reducedMotionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    let palette = readPalette(root);
    let waitingFrames = 0;
    let glows = new Map<string, HTMLCanvasElement>();
    const light = document.createElement("canvas");
    const reed = document.createElement("canvas");
    let scene: ReededGlassScene | null = null;
    let inView = true;
    let clock = 0;
    let lastTick = 0;
    let lastPaint = 0;
    let frame = 0;

    const glow = (color: string) => {
      let sprite = glows.get(color);
      if (!sprite) {
        sprite = createReededGlassGlow(document, color);
        glows.set(color, sprite);
      }
      return sprite;
    };

    const measure = () => {
      const bounds = root.getBoundingClientRect();
      const width = Math.max(1, bounds.width);
      const height = Math.max(1, bounds.height);
      const pixelRatio = reededGlassRenderScale;
      const target = anchor ? document.querySelector(anchor) : null;
      let anchorX = width * defaultAnchor.x;
      let anchorY = height * defaultAnchor.y;
      if (target) {
        const rect = target.getBoundingClientRect();
        anchorX = rect.left - bounds.left + rect.width / 2;
        anchorY = rect.top - bounds.top + rect.height / 2;
      }
      scene = { anchorX, anchorY, height, pixelRatio, width };
      const deviceWidth = Math.round(width * pixelRatio);
      const deviceHeight = Math.round(height * pixelRatio);
      if (canvas.width !== deviceWidth || canvas.height !== deviceHeight) {
        canvas.width = deviceWidth;
        canvas.height = deviceHeight;
      }
      if (reed.width !== deviceWidth || reed.height !== deviceHeight) {
        reed.width = deviceWidth;
        reed.height = deviceHeight;
      }
      // The rims in CSS fade the same way the canvas does.
      root.dataset.fade = reededGlassFadeAxis(scene);
      const lightSize = reededGlassLightSize(scene);
      if (light.width !== lightSize.width || light.height !== lightSize.height) {
        light.width = lightSize.width;
        light.height = lightSize.height;
      }
    };

    const moving = () =>
      motion === "swell" &&
      !reducedMotionQuery.matches &&
      inView &&
      !document.hidden;

    const paint = () => {
      if (!scene) measure();
      if (!scene) return;
      if (!palette) {
        // Styles can apply after mount (lazy route CSS); try again shortly.
        palette = readPalette(root);
        if (!palette) {
          if (!moving() && waitingFrames < 300) {
            waitingFrames += 1;
            window.requestAnimationFrame(() => refresh());
          }
          return;
        }
      }
      paintReededGlass(
        context,
        { glow, light, reed },
        palette,
        scene,
        clock,
        moving() ? "swell" : "still",
      );
      root.dataset.painted = "true";
    };

    const loop = (now: number) => {
      frame = 0;
      if (!moving()) return;
      clock += Math.min(now - lastTick, 100);
      lastTick = now;
      if (now - lastPaint >= reededGlassFrameIntervalMs) {
        lastPaint = now;
        paint();
      }
      frame = window.requestAnimationFrame(loop);
    };

    const refresh = () => {
      if (frame) window.cancelAnimationFrame(frame);
      frame = 0;
      paint();
      if (moving()) {
        lastTick = performance.now();
        frame = window.requestAnimationFrame(loop);
      }
    };

    const onResize = () => {
      measure();
      refresh();
    };
    const onTheme = () => {
      palette = readPalette(root);
      waitingFrames = 0;
      glows = new Map();
      refresh();
      // The base is the glass's own background, which may still be finishing
      // a (reduced-motion) transition; read it again once that frame is done.
      window.requestAnimationFrame(() => {
        palette = readPalette(root);
        glows = new Map();
        refresh();
      });
    };

    measure();
    refresh();

    const resizeObserver = new ResizeObserver(onResize);
    resizeObserver.observe(root);
    const anchorElement = anchor ? document.querySelector(anchor) : null;
    if (anchorElement) resizeObserver.observe(anchorElement);
    const themeObserver = new MutationObserver(onTheme);
    themeObserver.observe(document.documentElement, {
      attributeFilter: ["class", "data-theme", "data-accent"],
      attributes: true,
    });
    // A stylesheet added later can change the tokens the glass reads.
    themeObserver.observe(document.head, { childList: true });
    let intersectionObserver: IntersectionObserver | null = null;
    if ("IntersectionObserver" in window) {
      intersectionObserver = new IntersectionObserver(([entry]) => {
        inView = entry?.isIntersecting ?? true;
        refresh();
      });
      intersectionObserver.observe(root);
    }
    document.addEventListener("visibilitychange", refresh);
    reducedMotionQuery.addEventListener?.("change", refresh);

    return () => {
      if (frame) window.cancelAnimationFrame(frame);
      resizeObserver.disconnect();
      themeObserver.disconnect();
      intersectionObserver?.disconnect();
      document.removeEventListener("visibilitychange", refresh);
      reducedMotionQuery.removeEventListener?.("change", refresh);
    };
  }, [anchor, motion]);

  return (
    <div
      ref={rootRef}
      aria-hidden="true"
      className={cn("vt-reeded-glass", className)}
      data-counter-swell-cycle-ms={
        motion === "swell" ? reededGlassCounterSwellCycleMs : undefined
      }
      data-drift-cycle-ms={motion === "swell" ? reededGlassDriftCycleMs : undefined}
      data-motion={motion}
      data-reed-width={reededGlassReedWidth}
      data-swell-cycle-ms={motion === "swell" ? reededGlassSwellCycleMs : undefined}
    >
      <canvas ref={canvasRef} className="vt-reeded-glass__canvas" />
    </div>
  );
}
