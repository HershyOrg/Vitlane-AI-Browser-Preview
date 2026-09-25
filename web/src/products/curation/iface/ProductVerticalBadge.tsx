import { useLocale } from "../../../shared/i18n";

export function ProductVerticalBadge({ vertical }: { vertical?: string }) {
  const { l } = useLocale();
  if (!vertical) return null;
  const labels: Record<string, string> = {
    FASHION: l("Fashion", "패션"),
    BEAUTY: l("Beauty", "뷰티"),
    FOOD: l("Food", "식품"),
    LIVING: l("Home & living", "생활"),
    ELECTRONICS: l("Electronics", "전자제품"),
    GENERAL: l("General", "종합"),
  };
  return <span className="catalog-ui-target__vertical" aria-label={l("Product category: {category}", "상품군: {category}", { category: labels[vertical] ?? labels.GENERAL })}>{labels[vertical] ?? labels.GENERAL}</span>;
}
