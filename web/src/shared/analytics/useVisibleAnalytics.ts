import { useEffect, useRef } from "react";
import type { RefObject } from "react";
import { analyticsChanged, track, type Behavior } from "./analytics";

// Observe only explicitly selected product surfaces. No click/DOM-wide collection.
export function useVisibleAnalytics(ref: RefObject<HTMLElement | null>, event: Behavior, localKey: string, enabled = true) {
  const latest = useRef(event); latest.current = event;
  useEffect(() => {
    const node = ref.current;
    if (!enabled || !node || typeof IntersectionObserver === "undefined") return;
    let visible = false;
    const send = () => { if (visible) track(latest.current, localKey); };
    const observer = new IntersectionObserver(entries => {
      visible = entries.some(entry => entry.isIntersecting);
      send();
    }, { threshold: 0.01 });
    observer.observe(node);
    window.addEventListener(analyticsChanged, send);
    document.addEventListener("visibilitychange", send);
    return () => { observer.disconnect(); window.removeEventListener(analyticsChanged, send); document.removeEventListener("visibilitychange", send); };
  }, [ref, localKey, enabled]);
}
