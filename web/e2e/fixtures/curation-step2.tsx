// Test-only entry. Production builds use src/main.tsx.
import { createRoot } from "react-dom/client";
import { LocaleProvider, useLocale } from "../../src/shared/i18n";
import { TooltipProvider } from "../../src/shared/ui";
import { CurrentUserProvider } from "../../src/products/account/app/useCurrentUser";
import { PreferencesProvider } from "../../src/products/account/app/usePreferences";
import { WorkspaceArtifact } from "../../src/products/curation/iface/CurationWorkspacePage";
import { CatalogAPIUsage } from "../../src/products/curation/research/iface/CatalogAPIUsage";
import { PlanCreator } from "../../src/products/curation/planning/iface/PlanCreator";
import { initialPlanForm } from "../../src/products/curation/planning/domain/form";
import type { CurationWorkspaceResponse } from "../../src/products/curation/domain/types";
import fixture from "../../../shared/openapi/fixtures/curation-step2.v1.json";
import "../../src/styles.css";
import "../../src/product-shell.css";
import "../../src/order-operations.css";
import "../../src/catalog-surfaces.css";
import "../../src/account-surfaces.css";
// The registry malls render on the same card path as Coupang and 11st, so the
// browser matrix covers every mall label, price meaning and detail provenance.
const products = [fixture.observation, fixture.unknownObservation, ...fixture.mallObservations].map((externalObservation, i) => ({ candidateId: `candidate-${i}`, source: externalObservation.productRef.source, sourceProductRef: externalObservation.productRef, externalObservation, locator: { kind: "PRODUCT_URL", productUrl: externalObservation.productUrl }, features: [], specifications: [] }));
const response = { curation: { id: "curation-1", phase: "CURATING" }, plan: { locationContext: { country: "KR" }, totalBudget: { amount: "100000", currency: "KRW" } }, targets: [{ id: "target-1", title: "한국 상품", normalizedIntent: "라미 사파리 만년필", category: "문구", researchScope: { country: "KR" } }], research: { groups: [] }, cart: { selections: [] }, availableActions: [], timeline: [], latestArtifact: "CURATION_BOARD", catalogResearch: { schemaVersion: "vitlane.catalog-research-workspace.v3", pools: [{ targetId: "target-1", version: 1, expandOrdinal: 0, products, hiddenProducts: [] }], configurations: [], interactions: [] } } as unknown as CurationWorkspaceResponse;
const noop = async () => {};
function Fixture() { const { setLocale } = useLocale(); const mode = new URLSearchParams(location.search).get("mode"); return <main><nav><button id="en" onClick={() => setLocale("en-US")}>EN</button><button id="ko" onClick={() => setLocale("ko-KR")}>KO</button></nav>{mode === "plan" ? <PlanCreator initialForm={initialPlanForm} working={false} onSubmit={noop} /> : mode === "operator" ? <CatalogAPIUsage /> : <WorkspaceArtifact artifact={{ id: "artifact-1", kind: "CURATION", title: "Curation", summary: "Test fixture", updatedAt: "2026-09-11T00:00:00Z" }} response={response} working={false} catalogCartOpenRequest={0} onCatalogCartCountChange={() => {}} onResearchAgain={noop} onAddTargets={noop} onStartCurating={noop} onRemoveTarget={noop} />}</main>; }
createRoot(document.getElementById("root")!).render(<LocaleProvider><TooltipProvider><CurrentUserProvider><PreferencesProvider><Fixture /></PreferencesProvider></CurrentUserProvider></TooltipProvider></LocaleProvider>);
