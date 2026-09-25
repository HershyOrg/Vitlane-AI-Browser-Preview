import { useLayoutEffect, useRef, type RefObject } from "react";

/**
 * One modal layer over the page: a dialog, or a sheet that another dialog may open on top of.
 * While it is mounted everything else in `body` is inert and the page does not scroll; Escape and
 * Tab belong to the top-most open layer only, so a product's details opened from a Target's sheet
 * close first and return to the sheet. Focus goes back where it was when the layer closes.
 */
export function useModalLayer(panel: RefObject<HTMLElement | null>, onClose: () => void, initialFocus = "button") {
  const close = useRef(onClose); close.current = onClose;
  // Before paint: a layer that is visible already answers Escape, even to a key pressed in the same frame.
  useLayoutEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const backgrounds = Array.from(document.body.children).filter((el): el is HTMLElement => el instanceof HTMLElement && !el.contains(panel.current));
    const inert = backgrounds.map((el) => el.inert); backgrounds.forEach((el) => { el.inert = true; });
    panel.current?.querySelector<HTMLElement>(initialFocus)?.focus();
    const keydown = (event: KeyboardEvent) => {
      const openDialogs = Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"], [role="alertdialog"]')).filter((el) => el.getClientRects().length > 0);
      if (openDialogs.at(-1) !== panel.current) return;
      // A menu opened from this layer closes itself on Escape and marks the event handled.
      if (event.key === "Escape") { if (event.defaultPrevented) return; event.preventDefault(); close.current(); return; }
      if (event.key !== "Tab") return;
      const controls = Array.from(panel.current?.querySelectorAll<HTMLElement>('button:not(:disabled),a[href],input:not(:disabled),[tabindex="0"]') ?? []).filter((el) => el.getClientRects().length > 0);
      const first = controls[0], last = controls.at(-1);
      if (event.shiftKey && (document.activeElement === first || !panel.current?.contains(document.activeElement))) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    };
    document.addEventListener("keydown", keydown);
    return () => { document.removeEventListener("keydown", keydown); document.body.style.overflow = overflow;
      backgrounds.forEach((el, index) => { el.inert = inert[index]; });
      if (previous?.isConnected) previous.focus({ preventScroll: true }); };
  }, [panel, initialFocus]);
}
