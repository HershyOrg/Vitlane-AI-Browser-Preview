import { type ReactNode, useId, useRef } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { Button, sheetGrabberProps, useSwipeDismiss } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import { useModalLayer } from "../app/useModalLayer";
import "./candidate-presentation.css";
import "./curation-target-sheet.css";

// Same breakpoint as the candidate details sheet (candidate-presentation.css).
const targetSheetMedia = "(max-width: 52rem), (max-height: 35rem)";
const targetSideMedia = "(min-width: 52.0625rem) and (min-height: 35.0625rem)";

export type TargetSheetTab = { id: string; title: string; count: number };

/**
 * A product group's own surface (ADR-0086): its criteria, sort and every candidate. It slides in
 * from the right on a desktop and rises as a bottom sheet on a phone, so the conversation never has
 * to hold a board. A product's details are the same kind of sheet and open on top of it, a little
 * narrower (desktop) or shorter (phone), so this one stays visible behind them. Neither dims or blurs
 * the page, both can be dragged away, and both close by their ×, Escape or a click outside.
 */
export function TargetSheet({ tabs, activeId, onSelect, onClose, title, children }: {
  tabs: TargetSheetTab[]; activeId: string; onSelect: (id: string) => void; onClose: () => void;
  title: string; children: ReactNode;
}) {
  const { l } = useLocale();
  const panel = useRef<HTMLElement>(null);
  const backdrop = useRef<HTMLDivElement>(null);
  const titleId = useId(); const panelId = useId();
  // A bottom sheet leaves down and a side sheet leaves right. Touch drags start on the grabber or the
  // header so the list keeps its own scrolling; a mouse drags from the same places.
  const sheet = useSwipeDismiss(panel, { direction: "down", media: targetSheetMedia, backdropRef: backdrop, onDismiss: onClose, from: "handle" });
  const side = useSwipeDismiss(panel, { direction: "right", media: targetSideMedia, backdropRef: backdrop, onDismiss: onClose, from: "handle" });
  useModalLayer(panel, onClose, '[data-sheet-close="true"]');
  const moveTab = (from: number, step: number) => { const next = tabs[(from + step + tabs.length) % tabs.length]; onSelect(next.id); window.requestAnimationFrame(() => panel.current?.querySelector<HTMLElement>(`[data-sheet-tab="${CSS.escape(next.id)}"]`)?.focus()); };
  // No scrim: the sheet simply appears over the page (ADR-0086). The clear layer only catches a click outside it.
  return createPortal(<div ref={backdrop} className="curation-target-sheet__backdrop" role="presentation"
    onMouseDown={event => { if (event.target === event.currentTarget) onClose(); }}>
    <section ref={panel} className="curation-target-sheet" role="dialog" aria-modal="true" aria-labelledby={titleId} data-target-sheet={activeId}>
      {sheet ? <div {...sheetGrabberProps} /> : null}
      {side ? <div className="curation-side-grabber" data-swipe-dismiss="handle" aria-hidden="true" /> : null}
      <header className="curation-target-sheet__header" data-swipe-dismiss="handle">
        {/* With several product groups the tabs are the title; one group gets a plain heading. */}
        {tabs.length > 1 ? <h2 id={titleId} className="vt-visually-hidden">{title}</h2> : <h2 id={titleId} className="curation-target-sheet__title">{title}</h2>}
        {tabs.length > 1 && <div className="curation-target-sheet__tabs" role="tablist" aria-label={l("Product groups", "상품군")}>
          {tabs.map((tab, index) => <Button key={tab.id} type="button" emphasis="quiet" size="compact" role="tab" className="curation-target-sheet__tab" data-sheet-tab={tab.id}
            aria-selected={tab.id === activeId} aria-controls={panelId} tabIndex={tab.id === activeId ? 0 : -1}
            onClick={() => onSelect(tab.id)}
            onKeyDown={event => { if (event.key === "ArrowRight") { event.preventDefault(); moveTab(index, 1); } else if (event.key === "ArrowLeft") { event.preventDefault(); moveTab(index, -1); } }}>
            <span className="curation-target-sheet__tab-title">{tab.title}</span><span className="curation-target-sheet__tab-count">{tab.count}</span></Button>)}
        </div>}
        <Button type="button" size="compact" emphasis="quiet" className="curation-target-sheet__close" data-sheet-close="true" aria-label={l("Close", "닫기")} onClick={onClose}><X size={18} aria-hidden="true" /></Button>
      </header>
      <div id={panelId} className="curation-target-sheet__body" role={tabs.length > 1 ? "tabpanel" : undefined} aria-labelledby={tabs.length > 1 ? titleId : undefined}>{children}</div>
    </section>
  </div>, document.body);
}
