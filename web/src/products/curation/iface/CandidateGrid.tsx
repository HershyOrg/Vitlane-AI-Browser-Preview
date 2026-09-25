import { useVisibleAnalytics } from "../../../shared/analytics/useVisibleAnalytics";
import { Children, isValidElement, type CSSProperties, type ReactNode, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useMediaQueryMatch } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";

// Below this factor a scaled card stops being readable, so the grid keeps one
// full-size column instead (enlarged text, grids narrower than a 320px phone).
export const compactCardMinimumScale = 0.55;

// Phone layout, the same breakpoint as the drawer sidebar and bottom sheets.
export const candidateGridPhoneMedia = "(max-width: 52rem)";

// As many 13.5rem–15rem cards as the width holds, four at most. On a phone a grid too narrow for two
// such cards still shows two per row (ADR-0076): each card keeps the 15rem layout a phone used to show
// one of and is scaled down whole (`scale`), so it keeps its proportions instead of reflowing.
// Wider screens with enlarged text keep one full-size column.
export function candidateGridLayout(width: number, rem: number, gap = rem * 0.75, phone = false) {
  const readingWidth = rem * 13.5;
  const fullWidth = rem * 15;
  let columns = Math.max(1, Math.min(4, Math.floor((width + gap) / (readingWidth + gap))));
  let cardWidth = Math.max(0, Math.min(fullWidth, (width - gap * (columns - 1)) / columns));
  let scale = 1;
  const compactWidth = (width - gap) / 2;
  if (phone && columns === 1 && compactWidth / fullWidth >= compactCardMinimumScale) {
    columns = 2;
    cardWidth = compactWidth;
    scale = compactWidth / fullWidth;
  }
  return { columns, cardWidth, scale };
}

/**
 * Every candidate of one Target, top to bottom in the chosen sort (ADR-0086). The grid lives in the
 * Target's sheet, which scrolls; nothing here scrolls sideways. A sort change moves cards to their new
 * places instead of swapping them under the reader.
 */
export function CandidateGrid({ targetTitle, children }: { targetTitle: string; children: ReactNode }) {
  const { l } = useLocale();
  const gridRef = useRef<HTMLDivElement>(null);
  const items = Children.toArray(children);
  const previousPositions = useRef(new Map<string, { left: number; top: number }>());
  const orderSignature = items.map((c, i) => isValidElement(c) ? c.key ?? i : i).join("|");
  useVisibleAnalytics(gridRef, { name: "research_results_viewed" }, "results:" + orderSignature, items.length > 0);
  const [size, setSize] = useState({ width: 752, rem: 16, gap: 12 });
  const phone = useMediaQueryMatch(candidateGridPhoneMedia);
  const layout = candidateGridLayout(size.width, size.rem, size.gap, phone);
  useLayoutEffect(() => {
    const next = new Map<string, { left: number; top: number }>();
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    for (const cell of gridRef.current?.querySelectorAll<HTMLElement>(".catalog-ui-candidate-cell") ?? []) {
      const key = cell.dataset.candidateKey ?? ""; const point = { left: cell.offsetLeft, top: cell.offsetTop }; const old = previousPositions.current.get(key);
      if (!reduced && old && (point.left !== old.left || point.top !== old.top)) cell.animate?.([{ transform: `translate(${old.left - point.left}px, ${old.top - point.top}px)` }, { transform: "translate(0, 0)" }], { duration: 180, easing: "ease-out" });
      next.set(key, point);
    }
    previousPositions.current = next;
  }, [orderSignature, layout.columns]);
  useEffect(() => {
    const grid = gridRef.current;
    if (!grid) return;
    const measure = () => {
      const width = grid.clientWidth;
      const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
      // The CSS gap narrows on small screens; lay cards out with the real one.
      const gap = parseFloat(getComputedStyle(grid).columnGap) || rem * 0.75;
      if (width > 0) setSize(previous => previous.width === width && previous.rem === rem && previous.gap === gap ? previous : { width, rem, gap });
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(grid);
    window.addEventListener("resize", measure);
    return () => { observer?.disconnect(); window.removeEventListener("resize", measure); };
  }, []);
  const gridStyle = { "--candidate-columns": layout.columns, "--candidate-card-width": `${layout.cardWidth}px`, "--candidate-card-scale": layout.scale } as CSSProperties;
  return <div ref={gridRef} className="catalog-ui-candidate-grid" role="list" aria-label={l("Candidates for {target}", "{target} 후보", { target: targetTitle })}
    data-column-count={layout.columns} data-compact={layout.scale < 1 || undefined} style={gridStyle}>
    {items.map((child, index) => <div className="catalog-ui-candidate-cell" role="listitem" key={isValidElement(child) ? child.key ?? index : index}
      data-candidate-key={isValidElement(child) ? String(child.key ?? index) : String(index)} data-candidate-index={index}>{child}</div>)}
  </div>;
}
