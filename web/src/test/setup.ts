import { beforeEach } from "vitest";

Object.defineProperty(globalThis, "IS_REACT_ACT_ENVIRONMENT", {
  configurable: true,
  value: true,
});

// Existing component tests assert the long-standing Korean presentation, so
// every test starts as a Korean browser with no stored language (ADR-0080).
// Locale-specific tests set vt_locale_choice or navigator.languages explicitly.
beforeEach(() => {
  if (typeof document !== "undefined") {
    for (const name of ["vt_locale_choice", "vt_locale_seen"]) {
      document.cookie = `${name}=; Path=/; Max-Age=0`;
    }
    try { window.localStorage.removeItem("vitlane.locale.v2"); } catch { /* Storage may be unavailable. */ }
    Object.defineProperty(window.navigator, "languages", {
      configurable: true,
      get: () => ["ko-KR", "ko"],
    });
  }
});

if (typeof globalThis.ResizeObserver === "undefined") {
  class ResizeObserverStub implements ResizeObserver {
    disconnect() {}
    observe() {}
    unobserve() {}
  }

  Object.defineProperty(globalThis, "ResizeObserver", {
    configurable: true,
    value: ResizeObserverStub,
  });
}

if (
  typeof window !== "undefined" &&
  typeof window.matchMedia === "undefined"
) {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: (query: string) => ({
      addEventListener() {},
      addListener() {},
      dispatchEvent: () => false,
      matches: false,
      media: query,
      onchange: null,
      removeEventListener() {},
      removeListener() {},
    }),
  });
}

if (
  typeof Element !== "undefined" &&
  typeof Element.prototype.hasPointerCapture === "undefined"
) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
  Element.prototype.setPointerCapture = () => {};
}

if (
  typeof window !== "undefined" &&
  typeof window.PointerEvent === "undefined"
) {
  class PointerEventStub extends MouseEvent {
    pointerId = 1;
    pointerType = "mouse";
  }

  Object.defineProperty(window, "PointerEvent", {
    configurable: true,
    value: PointerEventStub,
  });
  Object.defineProperty(globalThis, "PointerEvent", {
    configurable: true,
    value: PointerEventStub,
  });
}
