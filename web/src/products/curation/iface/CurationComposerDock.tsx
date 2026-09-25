import { createContext, type ReactNode, useContext } from "react";
import { createPortal } from "react-dom";

/**
 * `undefined` means the component is rendered outside a CurationWorkspace
 * (component fixtures use that path). `null` means the workspace host has not
 * committed yet. Keeping those states distinct avoids painting the composer in
 * the artifact for one frame before moving it to the dock.
 */
export const CurationComposerHostContext = createContext<HTMLElement | null | undefined>(undefined);

/** Keep the contextual input last in DOM order without viewport-coordinate JS. */
export function CurationComposerDock({ children, progress }: { children: ReactNode; progress?: ReactNode }) {
  const host = useContext(CurationComposerHostContext);
  const dock = (
    <div className="catalog-ui-composer-dock">
      {progress && <div className="curation-composer-progress">{progress}</div>}
      {children}
    </div>
  );

  if (host === null) return null;
  if (host) return createPortal(dock, host);

  // Standalone component fixtures have no Workspace host. They keep the same
  // visual surface inline without reintroducing fixed coordinates.
  return <div className="curation-composer-anchor is-inline-fallback">{dock}</div>;
}
