import {
  type ReactNode,
  useEffect,
  useId,
  useRef,
} from "react";
import { Button, sheetGrabberProps, useSwipeDismiss } from "./ui";
import { useLocale } from "./i18n";

type Props = {
  children: ReactNode;
  description: string;
  eyebrow: string;
  onClose: () => void;
  title: string;
};

export function AppOverlay({
  children,
  description,
  eyebrow,
  onClose,
  title,
}: Props) {
  const { l } = useLocale();
  const titleID = useId();
  const descriptionID = useId();
  const panelRef = useRef<HTMLElement>(null);
  const backdropRef = useRef<HTMLDivElement>(null);
  // Narrow screens present the panel as a bottom sheet (product-shell.css).
  const sheet = useSwipeDismiss(panelRef, {
    direction: "down",
    media: "(max-width: 52rem)",
    backdropRef,
    onDismiss: onClose,
  });

  useEffect(() => {
    panelRef.current?.querySelector<HTMLButtonElement>(
      '[data-overlay-close="true"]',
    )?.focus();

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  return (
    <div
      ref={backdropRef}
      className="shell-overlay-backdrop vt-scrim"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) onClose();
      }}
    >
      <section
        ref={panelRef}
        className="shell-overlay-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        aria-describedby={descriptionID}
      >
        {sheet ? <div {...sheetGrabberProps} /> : null}
        <header className="shell-overlay-panel__header">
          <div>
            <p>{eyebrow}</p>
            <h2 id={titleID}>{title}</h2>
            <span id={descriptionID}>{description}</span>
          </div>
          <Button
            type="button"
            size="compact"
            emphasis="quiet"
            aria-label={l("Close panel", "패널 닫기")}
            data-overlay-close="true"
            onClick={onClose}
          >
            {l("Close", "닫기")}
          </Button>
        </header>
        <div className="shell-overlay-panel__body">{children}</div>
      </section>
    </div>
  );
}
