import type { Combination } from "./combination";
import type { LiveCartItem, LiveCatalogProduct, LiveVariantRow } from "../research/infra/liveCatalogReviewApi";
export type CombinationConfiguration = { variant: LiveVariantRow; observedAt: string; version: number };
export type CombinationSkipReason = "EXTERNAL" | "OPTION" | "PRICE" | "UNAVAILABLE" | "CHANGED" | "ALREADY";
export type CombinationSkip = { title: string; reason: CombinationSkipReason; candidateId: string; targetId: string };
export function selectCombinationCart(plan: Pick<Combination, "items">, products: ReadonlyMap<string, LiveCatalogProduct>, configurations: Readonly<Record<string, CombinationConfiguration>>, cart: readonly LiveCartItem[]) {
 const items: LiveCartItem[] = [], skipped: CombinationSkip[] = [];
 for (const item of plan.items ?? []) {
  const product = products.get(item.candidateId), configuration = configurations[item.candidateId], variant = configuration?.variant;
  const title = product?.title || "";
  let reason: CombinationSkipReason | undefined;
  if (!item.checkoutEligible || (product?.source && product.source !== "SHOPIFY")) reason = "EXTERNAL";
  else if (!product || (product.hydration && product.hydration.status !== "READY")) reason = "UNAVAILABLE";
  else if (!variant?.variantId || !Number.isFinite(Date.parse(configuration.observedAt))) reason = "OPTION";
  else if ((item.variantId && item.variantId !== variant.variantId) || (item.configurationVersion > 0 && item.configurationVersion !== configuration.version)) reason = "CHANGED";
  else if (!variant.available) reason = "UNAVAILABLE";
  else if (variant.priceUnknown || !Number.isSafeInteger(variant.priceMinor) || variant.priceMinor < 0 || !["USD", "KRW"].includes(variant.currency)) reason = "PRICE";
  else if (cart.some(row => row.candidateId === item.candidateId && row.variantId === variant.variantId) || items.some(row => row.candidateId === item.candidateId && row.variantId === variant.variantId)) reason = "ALREADY";
  if (reason || !product || !variant) { skipped.push({ title, reason: reason ?? "OPTION", candidateId: item.candidateId, targetId: item.targetId }); continue; }
  items.push({
   cartItemId: `cart:${item.candidateId}:${variant.variantId}`, targetId:item.targetId, candidateId:item.candidateId,
   productTitle:product.title, productUrl:variant.productUrl || product.locator?.productUrl, productImageUrl:variant.mediaUrl || product.mediaUrl,
   merchantName:product.previewVariant?.sellerName, sellerDomain:product.locator?.sellerDomain || product.previewVariant?.sellerDomain, intentPoint:product.intentPoint,
   variantId:variant.variantId, variantTitle:variant.title, selectedOptions:variant.selectedOptions.map(option=>`${option.name}: ${option.value}`),
   previewPriceMinor:variant.priceMinor, previewCurrency:variant.currency, quantity:item.quantity, observedAt:configuration.observedAt,
  });
 }
 return {items,skipped};
}
