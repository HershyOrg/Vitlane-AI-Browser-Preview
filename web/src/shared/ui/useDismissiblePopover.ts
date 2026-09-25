import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type MouseEvent,
} from "react";

const overlayOpenEvent = "vitlane:overlay-open";

type OverlayOpenDetail = {
  id: string;
};

export function useDismissiblePopover() {
  const id = useId();
  const rootRef = useRef<HTMLDetailsElement>(null);
  const [open, setOpen] = useState(false);

  const close = useCallback((restoreFocus = false) => {
    setOpen(false);
    if (restoreFocus) {
      window.requestAnimationFrame(() => {
        rootRef.current?.querySelector<HTMLElement>("summary")?.focus();
      });
    }
  }, []);

  const toggle = useCallback(() => {
    setOpen((current) => {
      const next = !current;
      if (next) {
        window.dispatchEvent(
          new CustomEvent<OverlayOpenDetail>(overlayOpenEvent, {
            detail: { id },
          }),
        );
      }
      return next;
    });
  }, [id]);

  useEffect(() => {
    function onPointerDown(event: PointerEvent) {
      if (!open || rootRef.current?.contains(event.target as Node)) return;
      close();
    }

    function onKeyDown(event: KeyboardEvent) {
      if (!open || event.key !== "Escape") return;
      event.preventDefault();
      close(true);
    }

    function onOtherOverlay(event: Event) {
      const detail = (event as CustomEvent<OverlayOpenDetail>).detail;
      if (detail?.id !== id) close();
    }

    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    window.addEventListener(overlayOpenEvent, onOtherOverlay);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
      window.removeEventListener(overlayOpenEvent, onOtherOverlay);
    };
  }, [close, id, open]);

  return {
    close,
    open,
    rootRef,
    summaryProps: {
      "aria-expanded": open,
      onClick: (event: MouseEvent<HTMLElement>) => {
        event.preventDefault();
        toggle();
      },
    },
    toggle,
  };
}
