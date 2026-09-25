import {
  useCallback,
  useEffect,
  useRef,
  useSyncExternalStore,
  type RefObject,
} from "react";

export type SwipeDismissDirection = "down" | "left" | "right";

export interface SwipeDismissOptions {
  /** The edge the surface leaves through: a bottom sheet leaves down, a left drawer left. */
  direction: SwipeDismissDirection;
  onDismiss: () => void;
  /** Media query in which the surface is presented as a sheet or drawer. */
  media?: string;
  enabled?: boolean;
  /** Scrim behind the surface. It fades with the drag and may contain the surface. */
  backdropRef?: RefObject<HTMLElement | null>;
  /** Touch drags start anywhere on the surface, or only inside `[data-swipe-dismiss="handle"]`. */
  from?: "surface" | "handle";
  /** "slide" leaves through the edge before dismissing; "settle" dismisses in place. */
  exit?: "slide" | "settle";
}

type Axis = { dx: number; dy: number };

// A drag is classified once it has moved this far. Browsers start native
// scrolling a little later, so the first classified touchmove is still cancelable.
export const swipeDecisionDistance = 6;
const dismissRatio = 0.25;
const flickVelocity = 0.45;
const flickMinimum = 12;
const motionMs = 220;
const releaseCleanupMs = 400;
const motionEasing = "cubic-bezier(0.32, 0.72, 0, 1)";
const noDragSelector =
  'input, textarea, select, [contenteditable="true"], [role="slider"], [data-swipe-dismiss="ignore"]';
const handleSelector = '[data-swipe-dismiss="handle"]';
// A handle may hold controls (a sheet header holds its close button and tabs). A press that starts on one
// is that control's, never a drag: a finger that slips a few pixels while tapping must still close the sheet.
const handleControlSelector = "button, a, [role='button'], [role='tab']";

/** Spread on a `div` as the first child of a bottom sheet that uses `useSwipeDismiss`. */
export const sheetGrabberProps = {
  className: "vt-sheet-grabber",
  "data-swipe-dismiss": "handle",
  "aria-hidden": true,
} as const;

/** Distance travelled toward the dismiss edge; negative when moving away from it. */
export function swipeTravel(direction: SwipeDismissDirection, { dx, dy }: Axis) {
  if (direction === "down") return dy;
  return direction === "left" ? -dx : dx;
}

/**
 * "pending" until the pointer has moved far enough to classify, then "drag"
 * when the movement heads for the dismiss edge more than across it, otherwise
 * "release" so the browser keeps the gesture (scrolling, other swipes).
 */
export function swipeDismissIntent(
  direction: SwipeDismissDirection,
  movement: Axis,
): "pending" | "drag" | "release" {
  if (Math.hypot(movement.dx, movement.dy) < swipeDecisionDistance) return "pending";
  const travel = swipeTravel(direction, movement);
  const across = Math.abs(direction === "down" ? movement.dx : movement.dy);
  return travel > 0 && travel > across ? "drag" : "release";
}

/** A quarter of the surface, or a quick flick toward the edge, dismisses it. */
export function shouldSwipeDismiss(offset: number, size: number, velocity: number) {
  return offset >= size * dismissRatio || (velocity >= flickVelocity && offset >= flickMinimum);
}

function subscribeToMedia(query: string | undefined, notify: () => void) {
  if (!query || typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return () => {};
  }
  const list = window.matchMedia(query);
  list.addEventListener?.("change", notify);
  return () => list.removeEventListener?.("change", notify);
}

function mediaMatches(query: string | undefined) {
  if (!query) return true;
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false;
  return window.matchMedia(query).matches;
}

export function useMediaQueryMatch(query: string | undefined) {
  const subscribe = useCallback((notify: () => void) => subscribeToMedia(query, notify), [query]);
  return useSyncExternalStore(subscribe, () => mediaMatches(query), () => !query);
}

// Scroll containers between the touch and the surface that could still move
// toward the dismiss edge. While any can, the drag belongs to that content.
function contentCanScrollToward(
  direction: SwipeDismissDirection,
  target: EventTarget | null,
  surface: HTMLElement,
) {
  for (let element = target instanceof Element ? target : null; element; element = element.parentElement) {
    if (element instanceof HTMLElement) {
      const style = window.getComputedStyle(element);
      const overflow = direction === "down" ? style.overflowY : style.overflowX;
      if (/(auto|scroll|overlay)/.test(overflow)) {
        if (direction === "down" && element.scrollHeight > element.clientHeight + 1 && element.scrollTop > 0) {
          return true;
        }
        const maxLeft = element.scrollWidth - element.clientWidth;
        if (direction === "left" && maxLeft > 1 && element.scrollLeft < maxLeft - 1) return true;
        if (direction === "right" && maxLeft > 1 && element.scrollLeft > 1) return true;
      }
    }
    if (element === surface) break;
  }
  return false;
}

function prefersReducedMotion() {
  return typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

type Gesture = {
  kind: "touch" | "pointer";
  id: number;
  x: number;
  y: number;
  state: "pending" | "drag";
  offset: number;
  size: number;
  samples: { time: number; offset: number }[];
};

type SavedStyles = {
  translate: string;
  transition: string;
  willChange: string;
  backdrop?: { opacity: string; transition: string };
};

/**
 * Lets a sheet or drawer follow a touch toward its edge and dismiss past a
 * quarter of its size or on a flick. Buttons, Escape and the scrim keep
 * working; the gesture only adds a direct way out. Touch events are used
 * (not pointer events) so a vertical scroller inside a bottom sheet keeps
 * native scrolling and hands the drag to the sheet only at its top edge.
 * Mouse and pen drags start on `[data-swipe-dismiss="handle"]` only, so text
 * selection and clicks inside the surface are untouched.
 */
export function useSwipeDismiss(
  surfaceRef: RefObject<HTMLElement | null>,
  {
    direction,
    onDismiss,
    media,
    enabled = true,
    backdropRef,
    from = "surface",
    exit = "slide",
  }: SwipeDismissOptions,
) {
  const matches = useMediaQueryMatch(media);
  const active = enabled && matches;
  const dismissRef = useRef(onDismiss);
  dismissRef.current = onDismiss;

  useEffect(() => {
    const surface = surfaceRef.current;
    if (!active || !surface) return;
    const backdrop = backdropRef?.current ?? null;
    let gesture: Gesture | null = null;
    let saved: SavedStyles | null = null;
    let timers: number[] = [];
    let suppressClickUntil = 0;
    // Drag styles still on screen after release: an animation back or out, or
    // a slid-out drawer waiting for its own closed state.
    let settleUntil = 0;

    const later = (callback: () => void, delay: number) => {
      timers.push(window.setTimeout(callback, delay));
    };
    const clearTimers = () => {
      timers.forEach((timer) => window.clearTimeout(timer));
      timers = [];
    };

    const sizeOf = () => {
      const bounds = surface.getBoundingClientRect();
      return Math.max(1, direction === "down" ? bounds.height : bounds.width);
    };
    const translateFor = (offset: number) =>
      direction === "down"
        ? `0px ${offset}px`
        : `${direction === "left" ? -offset : offset}px 0px`;

    const save = () => {
      if (saved) return;
      saved = {
        translate: surface.style.translate,
        transition: surface.style.transition,
        willChange: surface.style.willChange,
      };
      if (backdrop) {
        saved.backdrop = { opacity: backdrop.style.opacity, transition: backdrop.style.transition };
      }
      surface.style.willChange = "translate";
    };

    const restore = () => {
      if (!saved) return;
      surface.style.translate = saved.translate;
      surface.style.transition = saved.transition;
      surface.style.willChange = saved.willChange;
      if (backdrop && saved.backdrop) {
        backdrop.style.opacity = saved.backdrop.opacity;
        backdrop.style.transition = saved.backdrop.transition;
      }
      saved = null;
    };

    // A scrim is one darkening layer (.vt-scrim, never blurred), so it only
    // ever fades as a whole. Beside the surface (the sidebar) it follows the drag;
    // around the surface it stays until the surface leaves and fades with it.
    // visible: 1 is the resting scrim, 0 is gone.
    const followWithBackdrop = (visible: number) => {
      if (!backdrop || backdrop.contains(surface)) return;
      backdrop.style.opacity = String(Math.max(0, Math.min(1, visible)));
    };
    const fadeBackdropOut = () => {
      if (backdrop) backdrop.style.opacity = "0";
    };

    const place = (offset: number, animate: boolean) => {
      const transition = animate
        ? `translate ${motionMs}ms ${motionEasing}`
        : "none";
      surface.style.transition = transition;
      surface.style.translate = translateFor(offset);
      if (backdrop) {
        backdrop.style.transition = animate ? `opacity ${motionMs}ms ${motionEasing}` : "none";
      }
    };

    const begin = (kind: Gesture["kind"], id: number, x: number, y: number) => {
      clearTimers();
      restore();
      gesture = { kind, id, x, y, state: "pending", offset: 0, size: 1, samples: [] };
    };

    // Returns true when this movement belongs to the drag (caller cancels the native default).
    const move = (x: number, y: number, time: number, target: EventTarget | null, cancelable: boolean) => {
      if (!gesture) return false;
      const movement = { dx: x - gesture.x, dy: y - gesture.y };
      if (gesture.state === "pending") {
        const intent = swipeDismissIntent(direction, movement);
        if (intent === "pending") return false;
        if (intent === "release" || !cancelable || contentCanScrollToward(direction, target, surface)) {
          gesture = null;
          return false;
        }
        gesture.state = "drag";
        gesture.size = sizeOf();
        save();
      }
      const offset = Math.max(0, swipeTravel(direction, movement));
      gesture.offset = offset;
      gesture.samples.push({ time, offset });
      if (gesture.samples.length > 6) gesture.samples.shift();
      place(offset, false);
      followWithBackdrop(1 - offset / gesture.size);
      return true;
    };

    const settleBack = () => {
      place(0, true);
      followWithBackdrop(1);
      settleUntil = performance.now() + motionMs + 20;
      later(restore, motionMs + 20);
    };

    const finish = (cancelled: boolean, releasedAt: number) => {
      const current = gesture;
      gesture = null;
      if (!current || current.state !== "drag") return;
      suppressClickUntil = performance.now() + releaseCleanupMs;
      // Only movement in the last 100ms before release counts as a flick.
      const recent = current.samples.filter((sample) => sample.time >= releasedAt - 100);
      const first = recent[0];
      const last = recent.at(-1);
      const velocity = first && last && last.time > first.time
        ? (last.offset - first.offset) / (last.time - first.time)
        : 0;
      const reduced = prefersReducedMotion();
      if (cancelled || !shouldSwipeDismiss(current.offset, current.size, velocity)) {
        if (reduced) restore();
        else settleBack();
        return;
      }
      if (reduced) {
        // Both changes land before the next paint, so nothing jumps.
        dismissRef.current();
        restore();
        return;
      }
      if (exit === "settle") {
        settleBack();
        dismissRef.current();
        return;
      }
      place(current.size, true);
      fadeBackdropOut();
      settleUntil = performance.now() + motionMs + releaseCleanupMs;
      later(() => {
        dismissRef.current();
        // The surface normally unmounts or closes now. A drawer that only
        // changes class keeps the drag styles until its closed state has
        // taken over, so it never flashes back open.
        later(() => {
          if (surface.isConnected) restore();
        }, releaseCleanupMs);
      }, motionMs);
    };

    const onTouchStart = (event: TouchEvent) => {
      if (event.touches.length !== 1) {
        if (gesture?.state === "drag") finish(true, event.timeStamp);
        gesture = null;
        return;
      }
      const target = event.target;
      if (target instanceof Element && target.closest(noDragSelector)) return;
      if (from === "handle" && !(target instanceof Element && target.closest(handleSelector) && !target.closest(handleControlSelector))) return;
      const touch = event.touches[0];
      begin("touch", touch.identifier, touch.clientX, touch.clientY);
    };
    const onTouchMove = (event: TouchEvent) => {
      if (gesture?.kind !== "touch") return;
      const touch = Array.from(event.changedTouches).find((item) => item.identifier === gesture?.id);
      if (!touch) return;
      if (move(touch.clientX, touch.clientY, event.timeStamp, event.target, event.cancelable)) {
        event.preventDefault();
      }
    };
    const onTouchEnd = (event: TouchEvent) => {
      if (gesture?.kind !== "touch") return;
      if (!Array.from(event.changedTouches).some((item) => item.identifier === gesture?.id)) return;
      finish(event.type === "touchcancel", event.timeStamp);
    };

    const onPointerDown = (event: PointerEvent) => {
      if (event.pointerType === "touch" || event.button !== 0) return;
      if (!(event.target instanceof Element && event.target.closest(handleSelector)) || event.target.closest(handleControlSelector)) return;
      begin("pointer", event.pointerId, event.clientX, event.clientY);
      // Stop text selection while the handle is dragged.
      event.preventDefault();
    };
    const onPointerMove = (event: PointerEvent) => {
      if (gesture?.kind !== "pointer" || gesture.id !== event.pointerId) return;
      const pending = gesture.state === "pending";
      if (!move(event.clientX, event.clientY, event.timeStamp, event.target, true)) return;
      event.preventDefault();
      if (!pending) return;
      // Capture only once this is a drag: a press that never moves stays an ordinary click for the page.
      try {
        surface.setPointerCapture?.(event.pointerId);
      } catch {
        // Synthetic pointers have no capture target; the surface listeners still finish the drag.
      }
    };
    const onPointerEnd = (event: PointerEvent) => {
      if (gesture?.kind !== "pointer" || gesture.id !== event.pointerId) return;
      try {
        if (surface.hasPointerCapture?.(event.pointerId)) surface.releasePointerCapture(event.pointerId);
      } catch {
        // Capture may already be released by the browser.
      }
      finish(event.type === "pointercancel", event.timeStamp);
    };

    // A moved touch or drag is not a tap: swallow the click some browsers still send.
    const onClickCapture = (event: MouseEvent) => {
      if (performance.now() > suppressClickUntil) return;
      if (event.target instanceof Node && surface.contains(event.target)) {
        event.preventDefault();
        event.stopPropagation();
      }
    };

    surface.addEventListener("touchstart", onTouchStart, { passive: true });
    surface.addEventListener("touchmove", onTouchMove, { passive: false });
    surface.addEventListener("touchend", onTouchEnd, { passive: true });
    surface.addEventListener("touchcancel", onTouchEnd, { passive: true });
    surface.addEventListener("pointerdown", onPointerDown);
    surface.addEventListener("pointermove", onPointerMove);
    surface.addEventListener("pointerup", onPointerEnd);
    surface.addEventListener("pointercancel", onPointerEnd);
    document.addEventListener("click", onClickCapture, true);
    return () => {
      surface.removeEventListener("touchstart", onTouchStart);
      surface.removeEventListener("touchmove", onTouchMove);
      surface.removeEventListener("touchend", onTouchEnd);
      surface.removeEventListener("touchcancel", onTouchEnd);
      surface.removeEventListener("pointerdown", onPointerDown);
      surface.removeEventListener("pointermove", onPointerMove);
      surface.removeEventListener("pointerup", onPointerEnd);
      surface.removeEventListener("pointercancel", onPointerEnd);
      document.removeEventListener("click", onClickCapture, true);
      clearTimers();
      if (!surface.isConnected) return;
      // Dismissing usually turns the gesture off (the surface closes), which
      // runs this cleanup mid-animation. Let the release finish first.
      const remaining = settleUntil - performance.now();
      if (remaining > 0) window.setTimeout(() => { if (surface.isConnected) restore(); }, remaining);
      else restore();
    };
  }, [active, backdropRef, direction, exit, from, surfaceRef]);

  return active;
}
