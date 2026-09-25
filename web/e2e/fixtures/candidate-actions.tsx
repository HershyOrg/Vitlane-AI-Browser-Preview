// Local fixture only; production builds use src/main.tsx.
import { createRoot } from "react-dom/client";
import { Menu } from "lucide-react";
import { LocaleProvider } from "../../src/shared/i18n";
import { AppearanceProvider, Button, TooltipProvider } from "../../src/shared/ui";
import { ExternalProductCandidate } from "../../src/products/curation/iface/ExternalProductCandidate";
import { TargetComparison } from "../../src/products/curation/iface/ResearchComparison";
import type { LiveCatalogProduct } from "../../src/products/curation/research/infra/liveCatalogReviewApi";
import fixture from "../../../shared/openapi/fixtures/curation-step2.v1.json";
import "../../src/styles.css";
import "../../src/product-shell.css";
import "../../src/order-operations.css";
import "../../src/catalog-surfaces.css";
import "../../src/account-surfaces.css";
import "../../src/products/curation/iface/catalog-curation-research.css";
const observation = fixture.unknownObservation;
const product = { candidateId: "candidate-1", source: observation.productRef.source, title: observation.title, currency: "KRW", description: "", categories: [], features: [], specifications: [], sourceProductRef: observation.productRef, externalObservation: observation, axisAssessment: { schemaVersion: "vitlane.axis-assessment.v1", criteria: { axes: [] }, scores: [], weights: [], totalScore: 90, totalBasisPoints: 9000 } } as unknown as LiveCatalogProduct;
createRoot(document.getElementById("root")!).render(<LocaleProvider><AppearanceProvider><TooltipProvider><main className="shell-product-body">
  <Button className="shell-mobile-sidebar-trigger" emphasis="quiet" aria-label="Menu"><Menu /></Button>
  <TargetComparison products={[product]} cohort={[product]}>{products => <div className="phase8-product-rail">{products.map(value => <ExternalProductCandidate key={value.candidateId} product={value} curationId="curation-1" />)}</div>}</TargetComparison>
</main></TooltipProvider></AppearanceProvider></LocaleProvider>);
