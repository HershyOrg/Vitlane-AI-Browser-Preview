// @vitest-environment jsdom
import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  shouldSwipeDismiss,
  swipeDismissIntent,
  useSwipeDismiss,
  type SwipeDismissOptions,
} from "./useSwipeDismiss";

describe("swipe dismiss decisions", () => {
  it("waits for movement, then follows only the dismiss direction", () => {
    expect(swipeDismissIntent("down", { dx: 2, dy: 3 })).toBe("pending");
    expect(swipeDismissIntent("down", { dx: 2, dy: 9 })).toBe("drag");
    expect(swipeDismissIntent("down", { dx: 1, dy: -9 })).toBe("release");
    expect(swipeDismissIntent("down", { dx: 10, dy: 7 })).toBe("release");
    expect(swipeDismissIntent("left", { dx: -12, dy: 4 })).toBe("drag");
    expect(swipeDismissIntent("left", { dx: 12, dy: 0 })).toBe("release");
    expect(swipeDismissIntent("right", { dx: 12, dy: 3 })).toBe("drag");
    expect(swipeDismissIntent("right", { dx: 3, dy: 12 })).toBe("release");
  });

  it("dismisses past a quarter of the surface or on a flick", () => {
    expect(shouldSwipeDismiss(100, 400, 0)).toBe(true);
    expect(shouldSwipeDismiss(99, 400, 0.2)).toBe(false);
    expect(shouldSwipeDismiss(40, 400, 0.6)).toBe(true);
    expect(shouldSwipeDismiss(8, 400, 2)).toBe(false);
  });
});

type TouchPoint = { x: number; y: number };

function touch(target: Element, type: string, point: TouchPoint | null, timeStamp: number) {
  const event = new Event(type, { bubbles: true, cancelable: true });
  const touches = point ? [{ identifier: 1, clientX: point.x, clientY: point.y, target }] : [];
  const changed = point ? touches : [{ identifier: 1, clientX: 0, clientY: 0, target }];
  Object.defineProperty(event, "touches", { value: touches });
  Object.defineProperty(event, "changedTouches", { value: changed });
  Object.defineProperty(event, "timeStamp", { value: timeStamp });
  target.dispatchEvent(event);
  return event;
}

function Sheet(props: Partial<SwipeDismissOptions> & { onDismiss: () => void }) {
  const surface = useRef<HTMLElement>(null);
  const backdrop = useRef<HTMLDivElement>(null);
  const active = useSwipeDismiss(surface, { direction: "down", media: "(max-width: 52rem)", backdropRef: backdrop, ...props });
  return (
    <div ref={backdrop} data-testid="backdrop">
      <section ref={surface} data-active={active}>
        <div className="scroller">
          <p>content</p>
        </div>
      </section>
    </div>
  );
}

describe("useSwipeDismiss", () => {
  let container: HTMLDivElement;
  let root: Root;
  let sheetMedia = true;
  let reducedMotion = false;

  beforeEach(() => {
    vi.useFakeTimers();
    sheetMedia = true;
    reducedMotion = false;
    vi.spyOn(window, "matchMedia").mockImplementation((query: string) => ({
      matches: query.includes("prefers-reduced-motion") ? reducedMotion : sheetMedia,
      media: query,
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }));
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  async function mount(props: Partial<SwipeDismissOptions> & { onDismiss: () => void }) {
    await act(async () => root.render(<Sheet {...props} />));
    const surface = container.querySelector("section") as HTMLElement;
    surface.getBoundingClientRect = () => ({ x: 0, y: 400, width: 390, height: 400, top: 400, left: 0, right: 390, bottom: 800, toJSON: () => ({}) });
    const scroller = container.querySelector(".scroller") as HTMLElement;
    scroller.style.overflowY = "auto";
    return { surface, scroller };
  }

  it("follows a downward drag and dismisses after sliding out", async () => {
    const onDismiss = vi.fn();
    const { surface, scroller } = await mount({ onDismiss });
    expect(surface.dataset.active).toBe("true");
    touch(scroller, "touchstart", { x: 100, y: 100 }, 0);
    const move = touch(scroller, "touchmove", { x: 102, y: 180 }, 40);
    expect(move.defaultPrevented).toBe(true);
    expect(surface.style.translate).toBe("0px 80px");
    touch(scroller, "touchmove", { x: 102, y: 230 }, 400);
    touch(scroller, "touchend", null, 420);
    expect(surface.style.translate).toBe("0px 400px");
    expect(onDismiss).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(230); });
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("never starts a drag on a control inside a handle, and captures the mouse only for a real drag", async () => {
    const onDismiss = vi.fn();
    const { surface } = await mount({ onDismiss, from: "handle" });
    // A sheet header is a handle with its close button inside.
    const handle = document.createElement("header"); handle.dataset.swipeDismiss = "handle";
    const close = document.createElement("button"); handle.append(close); surface.prepend(handle);
    const capture = vi.fn(); surface.setPointerCapture = capture; surface.hasPointerCapture = () => false;
    const pointer = (target: Element, type: string, x: number, y: number, timeStamp: number) => {
      const event = new MouseEvent(type, { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 0 });
      Object.defineProperties(event, { pointerId: { value: 1 }, pointerType: { value: "mouse" }, timeStamp: { value: timeStamp } });
      target.dispatchEvent(event);
      return event;
    };
    // A press on the close button is the button's, even when the pointer slips toward the edge before release.
    const down = pointer(close, "pointerdown", 360, 420, 0);
    pointer(close, "pointermove", 361, 440, 20);
    pointer(close, "pointerup", 361, 440, 40);
    expect(down.defaultPrevented).toBe(false);
    expect(capture).not.toHaveBeenCalled();
    expect(surface.style.translate).toBe("");
    // The same for a finger: a tap that slips is still a tap, so the click is not swallowed.
    touch(close, "touchstart", { x: 360, y: 420 }, 50);
    touch(close, "touchmove", { x: 361, y: 440 }, 70);
    touch(close, "touchend", null, 90);
    expect(surface.style.translate).toBe("");
    // (The fake clock starts at zero, where "no drag has ended yet" and "now" would look the same.)
    await act(async () => { vi.advanceTimersByTime(10); });
    const click = new MouseEvent("click", { bubbles: true, cancelable: true });
    close.dispatchEvent(click);
    expect(click.defaultPrevented).toBe(false);
    // A drag from the handle is captured once it is classified as one, then follows and dismisses.
    pointer(handle, "pointerdown", 100, 410, 100);
    pointer(handle, "pointermove", 101, 413, 110);
    expect(capture).not.toHaveBeenCalled();
    pointer(handle, "pointermove", 102, 560, 200);
    expect(capture).toHaveBeenCalledTimes(1);
    expect(surface.style.translate).toBe("0px 150px");
    pointer(handle, "pointerup", 102, 560, 420);
    await act(async () => { vi.advanceTimersByTime(230); });
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("settles back and keeps the surface for a short, slow drag", async () => {
    const onDismiss = vi.fn();
    const { surface, scroller } = await mount({ onDismiss });
    touch(scroller, "touchstart", { x: 100, y: 100 }, 0);
    touch(scroller, "touchmove", { x: 100, y: 130 }, 300);
    touch(scroller, "touchmove", { x: 100, y: 140 }, 600);
    touch(scroller, "touchend", null, 900);
    expect(surface.style.translate).toBe("0px 0px");
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(onDismiss).not.toHaveBeenCalled();
    expect(surface.style.translate).toBe("");
  });

  it("dismisses on a quick flick even when short", async () => {
    const onDismiss = vi.fn();
    const { scroller } = await mount({ onDismiss });
    touch(scroller, "touchstart", { x: 100, y: 100 }, 0);
    touch(scroller, "touchmove", { x: 100, y: 110 }, 10);
    touch(scroller, "touchmove", { x: 100, y: 160 }, 60);
    touch(scroller, "touchend", null, 70);
    await act(async () => { vi.advanceTimersByTime(230); });
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it("leaves the drag to content that can still scroll up", async () => {
    const onDismiss = vi.fn();
    const { surface, scroller } = await mount({ onDismiss });
    Object.defineProperty(scroller, "scrollHeight", { value: 900 });
    Object.defineProperty(scroller, "clientHeight", { value: 300 });
    Object.defineProperty(scroller, "scrollTop", { value: 120 });
    touch(scroller, "touchstart", { x: 100, y: 100 }, 0);
    const move = touch(scroller, "touchmove", { x: 100, y: 260 }, 40);
    touch(scroller, "touchend", null, 60);
    expect(move.defaultPrevented).toBe(false);
    expect(surface.style.translate).toBe("");
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it("ignores upward drags and form controls", async () => {
    const onDismiss = vi.fn();
    const { surface, scroller } = await mount({ onDismiss });
    touch(scroller, "touchstart", { x: 100, y: 300 }, 0);
    expect(touch(scroller, "touchmove", { x: 100, y: 200 }, 40).defaultPrevented).toBe(false);
    touch(scroller, "touchend", null, 60);
    const input = document.createElement("input");
    scroller.append(input);
    touch(input, "touchstart", { x: 100, y: 100 }, 100);
    expect(touch(input, "touchmove", { x: 100, y: 300 }, 140).defaultPrevented).toBe(false);
    expect(surface.style.translate).toBe("");
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it("does nothing outside its media query", async () => {
    sheetMedia = false;
    const onDismiss = vi.fn();
    const { surface, scroller } = await mount({ onDismiss });
    expect(surface.dataset.active).toBe("false");
    touch(scroller, "touchstart", { x: 100, y: 100 }, 0);
    expect(touch(scroller, "touchmove", { x: 100, y: 300 }, 40).defaultPrevented).toBe(false);
    expect(surface.style.translate).toBe("");
  });

  it("dismisses without animation under reduced motion", async () => {
    reducedMotion = true;
    const onDismiss = vi.fn();
    const { surface, scroller } = await mount({ onDismiss });
    touch(scroller, "touchstart", { x: 100, y: 100 }, 0);
    touch(scroller, "touchmove", { x: 100, y: 300 }, 40);
    touch(scroller, "touchend", null, 60);
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(surface.style.translate).toBe("");
  });

  it("settle exit dismisses at once and animates the surface home", async () => {
    const onDismiss = vi.fn();
    const { surface, scroller } = await mount({ onDismiss, exit: "settle" });
    touch(scroller, "touchstart", { x: 100, y: 100 }, 0);
    touch(scroller, "touchmove", { x: 100, y: 260 }, 40);
    touch(scroller, "touchend", null, 60);
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(surface.style.translate).toBe("0px 0px");
  });
});
