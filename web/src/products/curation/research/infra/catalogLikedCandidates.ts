import { request } from "../../../../shared/api/client";
import type { ExternalProductObservation } from "../../domain/sourceProduct";

export type CatalogLikedCandidate = {
  key: string;
  curationId: string;
  candidateId: string;
  variantId?: string;
  variantTitle?: string;
  productTitle: string;
  productUrl?: string;
  merchant: string;
  priceMinor: number;
  priceUnknown?: boolean;
  currency: string;
  targetTitle: string;
  curationPath: string;
  updatedAt: string;
};

export const catalogLikedCandidatesChanged = "vitlane:phase8-liked-candidates-changed";

type ServerLikedVariant = Omit<CatalogLikedCandidate, "key" | "curationPath">;

export async function listCatalogLikedCandidates(): Promise<CatalogLikedCandidate[]> {
  const result = await request<{ candidates: ServerLikedVariant[]; products?: Array<{ curationId: string; candidateId: string; observation: ExternalProductObservation; updatedAt: string }> }>(
    "/api/v1/account/liked-variants",
  );
  const variants = result.candidates.map((candidate) => ({
    ...candidate,
    key: `${candidate.candidateId}::${candidate.variantId}`,
    curationPath: `/curations/${candidate.curationId}`,
  }));
  const products: CatalogLikedCandidate[] = (result.products ?? []).map(({ observation, ...candidate }) => ({
    ...candidate, key: `${candidate.candidateId}::product:${observation.productRef.productId}`,
    productTitle: observation.title, productUrl: observation.productUrl, merchant: observation.seller.name ?? "",
    priceMinor: observation.price.kind === "OBSERVED" ? observation.price.amountMinor ?? 0 : 0,
    priceUnknown: observation.price.kind !== "OBSERVED" || observation.price.amountMinor === undefined, currency: observation.price.currency ?? "KRW", targetTitle: "",
    curationPath: `/curations/${candidate.curationId}`,
  }));
  return [...variants, ...products].sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)).slice(0, 50);
}
